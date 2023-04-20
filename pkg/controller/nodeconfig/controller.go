package nodeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"time"

	ctlnode "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"

	nodeconfigv1 "github.com/harvester/node-manager/pkg/apis/node.harvesterhci.io/v1beta1"
	"github.com/harvester/node-manager/pkg/controller/nodeconfig/config"
	"github.com/harvester/node-manager/pkg/controller/nodeconfig/monitor"
	ctlv1 "github.com/harvester/node-manager/pkg/generated/controllers/node.harvesterhci.io/v1beta1"
)

const (
	HandlerName             = "harvester-node-config-controller"
	ConfigApplied           = "Applied"
	ConfigAppliedAnnotation = "AppliedConfig"
)

var toMonitorServices = []string{"NTP", "configFile"}

// ensure every monitor shutdown safely
var WaitGroup sync.WaitGroup

type Controller struct {
	ctx      context.Context
	NodeName string

	NodeConfigs      ctlv1.NodeConfigController
	NodeConfigsCache ctlv1.NodeConfigCache

	WaitGroup *sync.WaitGroup
}

func Register(ctx context.Context, nodeName string, nodecfg ctlv1.NodeConfigController, nodes ctlnode.NodeController) (*Controller, error) {
	ctl := &Controller{
		ctx:              ctx,
		NodeName:         nodeName,
		NodeConfigs:      nodecfg,
		NodeConfigsCache: nodecfg.Cache(),
		WaitGroup:        &WaitGroup,
	}

	monitorNnumbers := len(toMonitorServices)

	monitorModules := make([]interface{}, 0, monitorNnumbers)
	for _, serviceName := range toMonitorServices {
		monitorModule := monitor.InitServiceMonitor(ctx, nodecfg, nodes, nodeName, serviceName)
		monitorModules = append(monitorModules, monitorModule)
	}
	monitor.StartsAllMonitors(monitorModules)

	ctl.NodeConfigs.OnChange(ctx, HandlerName, ctl.OnNodeConfigChange)
	ctl.NodeConfigs.OnRemove(ctx, HandlerName, ctl.OnNodeConfigRemove)

	return ctl, nil
}

func (c *Controller) OnNodeConfigChange(key string, nodecfg *nodeconfigv1.NodeConfig) (*nodeconfigv1.NodeConfig, error) {
	confName := strings.Split(key, "/")[1]
	if nodecfg == nil || confName != c.NodeName || nodecfg.DeletionTimestamp != nil {
		logrus.Infof("Skip this round (OnChange) with NodeConfigs (%s): %+v", confName, nodecfg)
		return nil, nil
	}

	// NTP related handling
	appliedConfig := nodecfg.ObjectMeta.Annotations[ConfigAppliedAnnotation]
	ntpConfigHandler := config.NewNTPConfigHandler(nodecfg.Spec.NTPConfig, appliedConfig)
	updated, err := ntpConfigHandler.DoNTPUpdate(len(nodecfg.Status.NTPConditions) == 0)
	if err != nil {
		logrus.Errorf("Update NTP config fail. err: %v", err)
		return nil, err
	}
	if updated {
		if err := ntpConfigHandler.RestartService(); err != nil {
			logrus.Errorf("Restart systemd-timesyncd fail. err: %v", err)
			return nil, err
		}
		if err := ntpConfigHandler.UpdateNTPConfigPersistence(); err != nil {
			logrus.Errorf("Update NTP config to OEM fail. err: %v", err)
			return nil, err
		}

		annoValue := generateAnnotationValue(nodecfg.Spec.NTPConfig.NTPServers)
		bytes, err := json.Marshal(annoValue)
		if err != nil {
			logrus.Errorf("Marshal annotation value fail, err: %v", err)
			return nil, err
		}

		nodecfg, err := config.UpdateNTPConfigApplied(c.NodeConfigs, nodecfg)
		if err != nil {
			logrus.Errorf("Update NodeConfig Status fail, err: %v", err)
			return nil, err
		}

		nodecfgCpy := nodecfg.DeepCopy()
		if nodecfgCpy.ObjectMeta.Annotations == nil {
			nodecfgCpy.ObjectMeta.Annotations = make(map[string]string)
		}
		nodecfgCpy.ObjectMeta.Annotations[ConfigAppliedAnnotation] = string(bytes)
		if !reflect.DeepEqual(nodecfg, nodecfgCpy) {
			return c.NodeConfigs.Update(nodecfgCpy)
		}
	} else {
		logrus.Infof("NTP config is not changed")
	}

	return nil, nil
}

func (c *Controller) OnNodeConfigRemove(key string, nodecfg *nodeconfigv1.NodeConfig) (*nodeconfigv1.NodeConfig, error) {
	if nodecfg == nil || nodecfg.DeletionTimestamp == nil {
		logrus.Infof("Skip this round (OnRemove) with NodeConfigs :%+v", nodecfg)
		return nil, nil
	}

	confName := strings.Split(key, "/")[1]
	if confName != c.NodeName {
		return nil, fmt.Errorf("node name %s is not matched", confName)
	}

	logrus.Infof("Node config is removed, rollback and remove persistent NTP config")
	if err := config.NTPConfigRollback(); err != nil {
		logrus.Errorf("Rollback NTP config fail. err: %v", err)
		c.NodeConfigs.EnqueueAfter(nodecfg.Namespace, nodecfg.Name, enqueueJitter())
		return nil, err
	}
	if err := config.RemovePersistentNTPConfig(); err != nil {
		logrus.Errorf("Remove persistent NTP config fail. err: %v", err)
		c.NodeConfigs.EnqueueAfter(nodecfg.Namespace, nodecfg.Name, enqueueJitter())
		return nil, err
	}
	return nil, nil
}

func enqueueJitter() time.Duration {
	baseDelay := 7
	return time.Duration(rand.Intn(3)+baseDelay) * time.Second
}

func generateAnnotationValue(ntpServers string) *nodeconfigv1.AppliedConfigAnnotation {
	return &nodeconfigv1.AppliedConfigAnnotation{
		NTPServers: ntpServers,
	}
}
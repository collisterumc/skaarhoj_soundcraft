package main

import (
	ib "github.com/SKAARHOJ/ibeam-corelib-go"
	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	log "github.com/s00500/env_logger"
)

const coreName = "skaarhoj_soundcraft"
const coreVersion = "0.1.0"

func main() {
	ib.ReloadHook()

	log.Infof("%s started, version %s", coreName, coreVersion)

	coreInfo := &pb.CoreInfo{
		CoreVersion:    coreVersion,
		Description:    "Device core for Soundcraft Ui series digital mixers (Ui12, Ui16, Ui24R)",
		Label:          "Soundcraft Ui",
		Name:           coreName,
		DeviceCategory: pb.DeviceCategory_AudioDevice,
		ConnectionType: pb.ConnectionType_Network,
	}

	config := defaultConfig()

	manager, registry, toManager, fromManager := ib.CreateServerWithConfig(coreInfo, &config)

	registerModels(registry)
	configureParameters(registry)

	go processDevices(registry, config, fromManager, toManager)

	// Corelib resolves the final listen address itself (IBEAM_CORE_ADDRESS
	// override, unix socket on skaarOS production).
	log.Infof("Starting gRPC server (corelib resolves the listen address; default :8502)")
	manager.StartWithServer(":8502")
}

package main

import (
	skconfig "github.com/SKAARHOJ/ibeam-lib-config"
)

// CoreConfig is the TOML config structure exposed to Reactor via the corelib.
type CoreConfig struct {
	Devices []DeviceConfig `ibDispatch:"devices" ibDescription:"Configure your Soundcraft Ui mixers here"`
}

// DeviceConfig configures one mixer.
type DeviceConfig struct {
	skconfig.BaseDeviceConfig
	IP string `ibDispatch:"ip" ibValidate:"ip" ibOrder:"1" ibDescription:"IP address of the mixer (WebSocket, port 80)"`
}

func defaultConfig() CoreConfig {
	return CoreConfig{
		Devices: []DeviceConfig{
			{
				BaseDeviceConfig: skconfig.BaseDeviceConfig{
					DeviceID:    1,
					ModelID:     2, // Ui16
					Name:        "Ui16",
					Description: "Example mixer — set the IP and activate",
					Active:      false,
				},
				IP: "192.168.1.100",
			},
		},
	}
}

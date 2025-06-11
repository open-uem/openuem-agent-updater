//go:build darwin

package common

import (
	"log"
	"os/exec"

	openuem_utils "github.com/open-uem/utils"
	"gopkg.in/ini.v1"
)

func (us *UpdaterService) Watchdog() {
	var err error
	var restartRequired bool

	// Get conf file
	configFile := openuem_utils.GetAgentConfigFile()

	// Open ini file
	cfg, err := ini.Load(configFile)
	if err != nil {
		log.Println("[ERROR]: could not load config file")
		return
	}

	key, err := cfg.Section("Agent").GetKey("RestartRequired")
	if err != nil {
		log.Println("[ERROR]: could not get RestartRequired key")
	}

	restartRequired, err = key.Bool()
	if err != nil {
		log.Println("[ERROR]: could not parse RestartRequired")
		return
	}

	// Check if service is running
	if restartRequired {
		// Restart service
		if err := RestartService(); err != nil {
			return
		}

		cfg.Section("Agent").Key("RestartRequired").SetValue("false")
		if err := cfg.SaveTo(configFile); err != nil {
			log.Printf("[ERROR]: could not save RestartRequired to INI")
		}
		log.Printf("[INFO]: the agent has been restarted due to watchdog")
	} else {
		if !IsAgentServiceRunning() {
			// Start service
			if err := MacStartAgentService(); err != nil {
				log.Printf("[ERROR]: could not start openuem-agent service, reason: %v\n", err)
				return
			}
			log.Printf("[INFO]: the agent service was started as it wasn't running (fatal error?)")
		}
	}
}

func RestartService() error {
	// Stop service
	if err := MacStopAgentService(); err != nil {
		return err
	}

	// Start service
	if err := MacStartAgentService(); err != nil {
		return err
	}

	return nil
}

func MacStartAgentService() error {
	if err := exec.Command("launchctl", "load", "-w", "/Library/LaunchDaemons/openuem-agent.plist").Run(); err != nil {
		log.Printf("[ERROR]: could not start openuem-agent service, reason: %v\n", err)
		return err
	}
	return nil
}

func MacStopAgentService() error {
	if err := exec.Command("launchctl", "unload", "-w", "/Library/LaunchDaemons/openuem-agent.plist").Run(); err != nil {
		log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v\n", err)
		return err
	}

	return nil
}

func IsAgentServiceRunning() bool {
	_, err := exec.Command("launchctl", "list", "eu.openuem.openuem-agent").Output()
	if err != nil {
		return false
	}

	return true
}

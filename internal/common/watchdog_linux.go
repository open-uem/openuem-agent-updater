//go:build linux

package common

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

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
		if !IsAgentServiceRunning("openuem-agent") {
			// Create a backup of the agent's log before starting the service
			if err := os.Rename("/var/log/openuem-agent/openuem-agent.log", fmt.Sprintf("/var/log/openuem-agent/openuem-agent.%d.log", time.Now().Unix())); err != nil {
				log.Printf("[ERROR]: could not create a backup of the agent log")
			}

			// Start service
			if err := LinuxStartService("openuem-agent"); err != nil {
				log.Printf("[ERROR]: could not start openuem-agent service, reason: %v\n", err)
				return
			}
			log.Printf("[INFO]: the agent service was started as it wasn't running (fatal error?)")
		}
	}
}

func RestartService() error {
	// Stop service
	if err := LinuxStopService("openuem-agent"); err != nil {
		return err
	}

	// Start service
	if err := LinuxStartService("openuem-agent"); err != nil {
		return err
	}

	return nil
}

func LinuxStartService(service string) error {
	if err := exec.Command("systemctl", "start", service).Run(); err != nil {
		log.Printf("[ERROR]: could not start openuem-agent service, reason: %v\n", err)
		return err
	}
	return nil
}

func LinuxStopService(service string) error {
	if err := exec.Command("systemctl", "stop", service).Run(); err != nil {
		log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v\n", err)
		return err
	}

	return nil
}

func IsAgentServiceRunning(service string) bool {
	if err := exec.Command("systemctl", "is-active", "--quiet", service).Run(); err != nil {
		return false
	}

	return true
}

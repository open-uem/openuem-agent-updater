//go:build windows

package common

import (
	"fmt"
	"log"
	"os"
	"time"

	openuem_utils "github.com/open-uem/utils"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
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
			// Create a backup of the agent's log before starting the service
			if err := os.Rename("C:\\Program Files\\OpenUEM Agent\\logs\\openuem-log.txt", fmt.Sprintf("C:\\Program Files\\OpenUEM Agent\\logs\\openuem-log-%d.txt", time.Now().Unix())); err != nil {
				log.Printf("[ERROR]: could not create a backup of the agent log")
			}

			// Start service
			if err := openuem_utils.WindowsStartService("openuem-agent"); err != nil {
				log.Printf("[ERROR]: could not start openuem-agent service, reason: %v\n", err)
				return
			}
			log.Printf("[INFO]: the agent service was started as it wasn't running (fatal error?)")
		}
	}
}

func RestartService() error {
	// Stop service
	if err := openuem_utils.WindowsSvcControl("openuem-agent", svc.Stop, svc.Stopped); err != nil {
		log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v\n", err)
		return err
	}

	// Start service
	if err := openuem_utils.WindowsStartService("openuem-agent"); err != nil {
		// TODO: communicate this situation to the agent worker so it can show a warning
		log.Printf("[ERROR]: could not start openuem-agent service, reason: %v\n", err)
		return err
	}

	return nil
}

func IsAgentServiceRunning() bool {
	m, err := mgr.Connect()
	if err != nil {
		log.Println("[ERROR]: could not connect with service manager")
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService("openuem-agent")
	if err != nil {
		log.Println("[ERROR]: could not open openuem-agent service")
	}

	status, err := s.Query()
	if err != nil {
		log.Println("[ERROR]: could not get openuem-agent service status")
	}

	return svc.Running == status.State
}

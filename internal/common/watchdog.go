package common

import (
	"fmt"
	"log"
	"time"

	"github.com/go-co-op/gocron/v2"
	openuem_utils "github.com/open-uem/utils"
	"golang.org/x/sys/windows/svc"
	"gopkg.in/ini.v1"
)

func (us *UpdaterService) StartWatchdogJob() error {
	var err error

	us.WatchdogJob, err = us.TaskScheduler.NewJob(
		gocron.DurationJob(
			time.Duration(time.Duration(5*time.Minute)),
		),
		gocron.NewTask(func() {
			us.Watchdog()
		}),
	)
	if err != nil {
		return fmt.Errorf("could not start the Watchdog job: %v", err)
	}
	log.Printf("[INFO]: new Watchdog job has been scheduled every %d minutes", 5)
	return nil
}

func (us *UpdaterService) Watchdog() {
	var err error
	var restartRequired bool

	// Get conf file
	configFile := openuem_utils.GetConfigFile()

	// Open ini file
	cfg, err := ini.Load(configFile)
	if err != nil {
		log.Println("[ERROR]: could not load config file")
		return
	}

	key, err := cfg.Section("Agent").GetKey("RestartRequired")
	restartRequired, err = key.Bool()
	if err != nil {
		log.Println("[ERROR]: could not parse RestartRequired")
		return
	}

	if restartRequired {
		// Stop service
		if err := RestartService(); err != nil {
			return
		}

		cfg.Section("Agent").Key("RestartRequired").SetValue("false")
		if err := cfg.SaveTo(configFile); err != nil {
			log.Printf("[ERROR]: could not save RestartRequired to INI")
		}
		log.Printf("[INFO]: the agent has been restarted due to watchdog")
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
		log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v\n", err)
		return err
	}

	return nil
}

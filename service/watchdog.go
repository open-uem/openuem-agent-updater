package service

import (
	"fmt"
	"log"
	"time"

	"github.com/doncicuto/openuem_utils"
	"github.com/go-co-op/gocron/v2"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
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

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\OpenUEM\Agent`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		log.Println("[ERROR]: agent cannot read the agent hive")
	}
	defer k.Close()

	restartValue, _, err := k.GetIntegerValue("RestartRequired")
	if err == nil {
		restartRequired = restartValue == 1
	}

	if restartRequired {
		// Stop service
		if err := openuem_utils.WindowsSvcControl("openuem-agent", svc.Stop, svc.Stopped); err != nil {
			log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v\n", err)
			return
		}

		// Start service
		if err := openuem_utils.WindowsStartService("openuem-agent"); err != nil {
			// TODO: communicate this situation to the agent worker so it can show a warning
			log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v\n", err)
			return
		}

		// Reset value in registry
		if err := k.SetDWordValue("RestartRequired", 0); err != nil {
			log.Printf("[ERROR]: could not save RestartRequired to the registry")
		}
		log.Printf("[INFO]: the agent has been restarted due to watchdog")
	}
}

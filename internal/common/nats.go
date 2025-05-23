package common

import (
	"fmt"
	"log"
	"time"

	"github.com/go-co-op/gocron/v2"
	openuem_nats "github.com/open-uem/nats"
)

func (us *UpdaterService) StartNATSConnectJob(queueSubscribe func() error) error {
	var err error

	us.NATSConnection, err = openuem_nats.ConnectWithNATS(us.NATSServers, us.AgentCert, us.AgentKey, us.CACert)
	if err == nil {
		if err := queueSubscribe(); err == nil {
			return err
		}
		return nil
	}
	log.Printf("[ERROR]: could not connect to NATS %v", err)

	us.NATSConnectJob, err = us.TaskScheduler.NewJob(
		gocron.DurationJob(
			time.Duration(time.Duration(2*time.Minute)),
		),
		gocron.NewTask(
			func() {
				if us.NATSConnection == nil {
					us.NATSConnection, err = openuem_nats.ConnectWithNATS(us.NATSServers, us.AgentCert, us.AgentKey, us.CACert)
					if err != nil {
						log.Printf("[ERROR]: could not connect to NATS %v", err)
						return
					}
				}

				if err := queueSubscribe(); err != nil {
					return
				}

				if err := us.TaskScheduler.RemoveJob(us.NATSConnectJob.ID()); err != nil {
					return
				}
			},
		),
	)
	if err != nil {
		return fmt.Errorf("could not start the NATS connect job: %v", err)
	}
	log.Printf("[INFO]: new NATS connect job has been scheduled every %d minutes", 2)
	return nil
}

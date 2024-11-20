package service

import (
	"context"

	"github.com/doncicuto/openuem_utils"
	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
)

const NATS_TOKEN = "OpenUEM"

type UpdaterService struct {
	AgentId                string
	NATSConnection         *nats.Conn
	NATSConnectJob         gocron.Job
	NATSServers            string
	TaskScheduler          gocron.Scheduler
	Logger                 *openuem_utils.OpenUEMLogger
	JetstreamContextCancel context.CancelFunc
}

func NewUpdateService() (*UpdaterService, error) {
	var err error
	us := UpdaterService{}
	us.Logger = openuem_utils.NewLogger("openuem-updater-service.txt")

	us.TaskScheduler, err = gocron.NewScheduler()
	if err != nil {
		return nil, err
	}

	return &us, nil
}

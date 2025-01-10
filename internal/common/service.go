package common

import (
	"context"

	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	openuem_utils "github.com/open-uem/utils"
)

type UpdaterService struct {
	AgentId                string
	NATSConnection         *nats.Conn
	NATSConnectJob         gocron.Job
	WatchdogJob            gocron.Job
	NATSServers            string
	TaskScheduler          gocron.Scheduler
	Logger                 *openuem_utils.OpenUEMLogger
	JetstreamContextCancel context.CancelFunc
	AgentCert              string
	AgentKey               string
	CACert                 string
}

func NewUpdateService() (*UpdaterService, error) {
	var err error
	us := UpdaterService{}
	us.Logger = openuem_utils.NewLogger("openuem-agent-updater.txt")

	us.TaskScheduler, err = gocron.NewScheduler()
	if err != nil {
		return nil, err
	}

	return &us, nil
}

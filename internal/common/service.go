package common

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	openuem_nats "github.com/open-uem/nats"
	openuem_utils "github.com/open-uem/utils"
	"gopkg.in/ini.v1"
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

func (us *UpdaterService) StartService() {
	// Start the task scheduler
	us.TaskScheduler.Start()
	log.Println("[INFO]: task scheduler has been started")

	// Start NATS connection job
	if err := us.StartNATSConnectJob(us.queueSubscribe); err != nil {
		return
	}

	// Start Watchdog job
	if err := us.StartWatchdogJob(); err != nil {
		return
	}
}

func (us *UpdaterService) StopService() {
	if us.Logger != nil {
		us.Logger.Close()
	}

	if us.NATSConnection != nil {
		if err := us.NATSConnection.Flush(); err != nil {
			log.Println("[ERROR]: could not flush NATS connection")
		}
		us.NATSConnection.Close()
	}
}

func (us *UpdaterService) queueSubscribe() error {
	var ctx context.Context

	js, err := jetstream.New(us.NATSConnection)
	if err != nil {
		log.Printf("[ERROR]: could not instantiate JetStream: %s", err.Error())
		return err
	}
	log.Println("[INFO]: JetStream has been instantiated")

	ctx, us.JetstreamContextCancel = context.WithTimeout(context.Background(), 60*time.Minute)
	s, err := js.Stream(ctx, "AGENTS_STREAM")
	if err != nil {
		log.Printf("[ERROR]: could not create stream AGENTS_STREAM: %v\n", err)
		return err
	}

	consumerConfig := jetstream.ConsumerConfig{
		Durable:        "AgentUpdater" + us.AgentId,
		FilterSubjects: []string{"agent.update." + us.AgentId},
	}

	if len(strings.Split(us.NATSServers, ",")) > 1 {
		consumerConfig.Replicas = int(math.Min(float64(len(strings.Split(us.NATSServers, ","))), 5))
	}

	c1, err := s.CreateOrUpdateConsumer(ctx, consumerConfig)
	if err != nil {
		log.Printf("[ERROR]: could not create Jetstream consumer: %s", err.Error())
		return err
	}
	// TODO stop consume context ()
	_, err = c1.Consume(us.JetStreamUpdaterHandler, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		log.Printf("[ERROR]: consumer error: %s", err.Error())
	}))

	log.Println("[INFO]: Jetstream created and started consuming messages")
	log.Println("[INFO]: subscribed to message ", fmt.Sprintf("agent.update.%s", us.AgentId))

	// Subscribe to agent restart
	_, err = us.NATSConnection.QueueSubscribe("agent.restart."+us.AgentId, "openuem-agent-management", us.restartHandler)
	if err != nil {
		log.Printf("[ERROR]: could not subscribe to NATS message, reason: %v", err)
		return err
	}
	log.Printf("[INFO]: subscribed to message agent.restart")

	return nil
}

func (us *UpdaterService) JetStreamUpdaterHandler(msg jetstream.Msg) {
	if msg.Subject() == fmt.Sprintf("agent.update.%s", us.AgentId) {
		us.updateHandler(msg)
	}
}

func (us *UpdaterService) restartHandler(msg *nats.Msg) {
	if err := RestartService(); err != nil {
		return
	}
	log.Println("[INFO]: agent has been forced to restart")

	if err := msg.Respond(nil); err != nil {
		log.Println("[ERROR]: could not respond to force restart request")
	}
}

func (us *UpdaterService) updateHandler(msg jetstream.Msg) {
	data := openuem_nats.OpenUEMUpdateRequest{}

	if err := json.Unmarshal(msg.Data(), &data); err != nil {
		log.Printf("[ERROR]: could not unmarshal update request, reason: %v\n", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not unmarshal update request, reason: %v", err))
		return
	}

	// If scheduled time is in the past execute now
	if !time.Time.IsZero(data.UpdateAt) && data.UpdateAt.Before(time.Now().Local()) {
		data.UpdateNow = true
	}

	if data.UpdateNow {
		if _, err := us.TaskScheduler.NewJob(
			gocron.OneTimeJob(
				gocron.OneTimeJobStartImmediately(),
			),
			gocron.NewTask(
				func() {
					ExecuteUpdate(data, msg)
				},
			),
		); err != nil {
			log.Printf("[ERROR]: could not schedule the update task: %v\n", err)
			msg.NakWithDelay(60 * time.Minute)
			SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not schedule the update task: %v", err))
			return
		}
		log.Println("[INFO]: new update task will run now")
	} else {
		if !time.Time.IsZero(data.UpdateAt) {
			if _, err := us.TaskScheduler.NewJob(
				gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(data.UpdateAt)),
				gocron.NewTask(func() {
					ExecuteUpdate(data, msg)
				}),
			); err != nil {
				log.Printf("[ERROR]: could not schedule the update task: %v\n", err)
				msg.NakWithDelay(60 * time.Minute)
				SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not schedule the update task: %v", err))
				return
			}
			log.Printf("[INFO]: new update task scheduled a %s", data.UpdateAt.String())
		}
	}

	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not sent ACK, reason: %v", err)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not sent ACK, reason: %v", err))
		return
	}

	return
}

func SaveTaskInfoToINI(status, result string) {

	// Get conf file
	configFile := openuem_utils.GetAgentConfigFile()

	// Open ini file
	cfg, err := ini.Load(configFile)
	if err != nil {
		log.Println("[ERROR]: could not load config file")
		return
	}

	cfg.Section("Agent").Key("UpdaterLastExecutionTime").SetValue(time.Now().Local().Format("2006-01-02T15:04:05"))
	cfg.Section("Agent").Key("UpdaterLastExecutionStatus").SetValue(status)
	cfg.Section("Agent").Key("UpdaterLastExecutionResult").SetValue(result)
	if err := cfg.SaveTo(configFile); err != nil {
		log.Println("[ERROR]: could not save update task info to INI file")
		return
	}
}

func (us *UpdaterService) ReadConfig() error {
	var err error

	// Get conf file
	configFile := openuem_utils.GetAgentConfigFile()

	// Open ini file
	cfg, err := ini.Load(configFile)
	if err != nil {
		return err
	}

	key, err := cfg.Section("Agent").GetKey("UUID")
	if err != nil {
		log.Println("[ERROR]: could not get UUID")
		return err
	}
	us.AgentId = key.String()

	key, err = cfg.Section("NATS").GetKey("NATSServers")
	if err != nil {
		log.Println("[ERROR]: could not get NATSServers")
		return err
	}
	us.NATSServers = key.String()

	// Read required certificates and private key either from config file or
	// reading from the current directory
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Fatalf("[FATAL]: could not get current working directory")
	}

	// CA Cert
	key, err = cfg.Section("Certificates").GetKey("CACert")
	if err != nil {
		log.Println("[ERROR]: could not get CA certificate from config file")
		us.CACert = filepath.Join(cwd, "certificates", "ca.cer")
	} else {
		us.CACert = key.String()
	}

	_, err = openuem_utils.ReadPEMCertificate(us.CACert)
	if err != nil {
		log.Fatalf("[FATAL]: could not read CA certificate")
	}

	// Agent cert
	key, err = cfg.Section("Certificates").GetKey("AgentCert")
	if err != nil {
		log.Println("[ERROR]: could not get agent certificate from config file")
		us.AgentCert = filepath.Join(cwd, "certificates", "agent.cer")
	} else {
		us.AgentCert = key.String()
	}

	_, err = openuem_utils.ReadPEMCertificate(us.AgentCert)
	if err != nil {
		log.Fatalf("[FATAL]: could not read agent certificate")
	}

	// Agent key
	key, err = cfg.Section("Certificates").GetKey("AgentKey")
	if err != nil {
		log.Println("[ERROR]: could not get agent private key from config file")
		us.AgentKey = filepath.Join(cwd, "certificates", "agent.cer")
	} else {
		us.AgentKey = key.String()
	}

	_, err = openuem_utils.ReadPEMPrivateKey(us.AgentKey)
	if err != nil {
		log.Fatalf("[FATAL]: could not read agent private key")
	}

	return nil
}

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

//go:build windows

package common

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/doncicuto/openuem_nats"
	"github.com/doncicuto/openuem_utils"
	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/sys/windows/svc"
	"gopkg.in/ini.v1"
)

func (us *UpdaterService) StartWindowsService() {
	// Start the task scheduler
	us.TaskScheduler.Start()
	log.Println("[INFO]: task scheduler has been started")

	// Start NATS connection job
	if err := us.StartNATSConnectJob(us.queueSubscribeForWindows); err != nil {
		return
	}

	// Start Watchdog job
	if err := us.StartWatchdogJob(); err != nil {
		return
	}
}

func (us *UpdaterService) StopWindowsService() {
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

func (us *UpdaterService) JetStreamUpdaterHandler(msg jetstream.Msg) {
	if msg.Subject() == fmt.Sprintf("agent.update.%s", us.AgentId) {
		us.updateHandlerForWindows(msg)
	}
}

func (us *UpdaterService) queueSubscribeForWindows() error {
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

	c1, err := s.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:        "AgentUpdater" + us.AgentId,
		FilterSubjects: []string{"agent.update." + us.AgentId},
	})
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

func (us *UpdaterService) restartHandler(msg *nats.Msg) {
	if err := RestartService(); err != nil {
		return
	}
	log.Println("[INFO]: agent has been forced to restart")

	if err := msg.Respond(nil); err != nil {
		log.Println("[ERROR]: could not respond to force restart request")
	}
}

func (us *UpdaterService) updateHandlerForWindows(msg jetstream.Msg) {
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
	configFile := openuem_utils.GetConfigFile()

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

func ExecuteUpdate(data openuem_nats.OpenUEMUpdateRequest, msg jetstream.Msg) {
	// Download the file
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Printf("[ERROR]: could not get working directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not get working directory, reason %v", err))
		return
	}

	// TODO - find a better place to save the agent installer and manage certificate location
	downloadPath := filepath.Join(cwd, "certificates", "download.exe")
	if err := openuem_utils.DownloadFile(data.DownloadFrom, downloadPath, data.DownloadHash); err != nil {
		log.Printf("[ERROR]: could not download update to directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not download update to directory, reason %v\n", err))
		return
	}

	// Stop service
	if err := openuem_utils.WindowsSvcControl("openuem-agent", svc.Stop, svc.Stopped); err != nil {
		log.Printf("[ERROR]: %v", err)
	}

	SaveTaskInfoToINI(openuem_nats.UPDATE_SUCCESS, "")
	log.Println("[INFO]: new OpenUEM Agent update command was called", downloadPath)
	msg.Ack()

	cmd := exec.Command(downloadPath, "/VERYSILENT")
	err = cmd.Start()
	if err != nil {
		log.Printf("[ERROR]: could not run %s command, reason: %v", downloadPath, err)
		return
	}
}

func (us *UpdaterService) AgentRollback(msg jetstream.Msg) {
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not send ACK for every agent rollback, reason: %v", err)
		}
		return
	}

	rollbackPath := filepath.Join(cwd, "updater", "rollback.exe")
	agentPath := filepath.Join(cwd, "openuem-agent.exe")

	// Preparing for rollback
	if _, err := os.Stat(rollbackPath); err != nil {
		log.Printf("[ERROR]: could not find previous OpenUEM Agent, reason %v", err)
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not send ACK for every agent rollback, reason: %v", err)
		}
		return
	}

	// Stop service
	if err := openuem_utils.WindowsSvcControl("openuem-agent", svc.Stop, svc.Stopped); err != nil {
		log.Printf("[ERROR]: could not stop openuem-agent service, reason: %v", err)
	}

	// Rename rollback agent to agent
	if err := os.Rename(rollbackPath, agentPath); err != nil {
		log.Printf("[ERROR]: could not overwrite agent executable with rollback, reason: %v", err)
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not send ACK for every agent rollback, reason: %v", err)
		}
		return
	}

	// Start service
	if err := openuem_utils.WindowsStartService("openuem-agent"); err != nil {
		log.Printf("[ERROR]: could not start new agent service using rollback, reason: %v", err)
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not send ACK for every agent rollback, reason: %v", err)
		}
		return
	}

	log.Println("[INFO]: OpenUEM Agent was rolled back")
	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not send ACK for every agent rollback, reason: %v", err)
	}
}

func (us *UpdaterService) ReadWindowsConfig() error {
	var err error

	// Get conf file
	configFile := openuem_utils.GetConfigFile()

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

	// Read required certificates and private key
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Fatalf("[FATAL]: could not get current working directory")
	}

	us.AgentCert = filepath.Join(cwd, "certificates", "agent.cer")
	_, err = openuem_utils.ReadPEMCertificate(us.AgentCert)
	if err != nil {
		log.Fatalf("[FATAL]: could not read agent certificate")
	}

	us.AgentKey = filepath.Join(cwd, "certificates", "agent.key")
	_, err = openuem_utils.ReadPEMPrivateKey(us.AgentKey)
	if err != nil {
		log.Fatalf("[FATAL]: could not read agent private key")
	}

	us.CACert = filepath.Join(cwd, "certificates", "ca.cer")
	_, err = openuem_utils.ReadPEMCertificate(us.CACert)
	if err != nil {
		log.Fatalf("[FATAL]: could not read CA certificate")
	}

	return nil
}

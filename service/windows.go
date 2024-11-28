//go:build windows

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/doncicuto/openuem_nats"
	"github.com/doncicuto/openuem_utils"
	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
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
	if msg.Subject() == fmt.Sprintf("agentupdate.%s", us.AgentId) {
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
	s, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "UPDATER_STREAM_" + us.AgentId,
		Subjects: []string{fmt.Sprintf("agentupdate.%s", us.AgentId)},
	})
	if err != nil {
		log.Printf("[ERROR]: could not create stream: %s\n", err.Error())
		return err
	}
	log.Println("[INFO]: stream has been created")

	c1, err := s.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   "UpdaterConsumer",
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		log.Printf("[ERROR]: could not create Jetstream consumer: %s", err.Error())
		return err
	}
	// TODO stop consume context ()
	_, err = c1.Consume(us.JetStreamUpdaterHandler, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		log.Printf("[ERROR]: consumer error: %s", err.Error())
	}))

	log.Printf("[INFO]: Jetstream created and started consuming messages")

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
		SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not unmarshal update request, reason: %v", err))
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
			SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not schedule the update task: %v", err))
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
				SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not schedule the update task: %v", err))
				return
			}
			log.Printf("[INFO]: new update task scheduled a %s", data.UpdateAt.String())
		}
	}

	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not sent ACK, reason: %v", err)
		SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not sent ACK, reason: %v", err))
		return
	}

	return
}

func SaveTaskInfoToRegistry(status, result string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\OpenUEM\Agent`, registry.SET_VALUE)
	if err != nil {
		log.Println("[ERROR]: agent cannot read the agent hive")
	}
	defer k.Close()

	if err := k.SetStringValue("UpdaterLastExecutionTime", time.Now().Local().Format("2006-01-02T15:04:05")); err != nil {
		log.Printf("[ERROR]: could not save execution time to the registry")
	}
	if err := k.SetStringValue("UpdaterLastExecutionStatus", status); err != nil {
		log.Printf("[ERROR]: could not save execution status to the registry")
	}
	if err := k.SetStringValue("UpdaterLastExecutionResult", result); err != nil {
		log.Printf("[ERROR]: could not save execution result to the registry")
	}
}

func ExecuteUpdate(data openuem_nats.OpenUEMUpdateRequest, msg jetstream.Msg) {
	// Download the file
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Printf("[ERROR]: could not get working directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not get working directory, reason %v", err))
		return
	}

	downloadPath := filepath.Join(cwd, "updater", "download.exe")
	if err := openuem_utils.DownloadFile(data.DownloadFrom, downloadPath, data.DownloadHash); err != nil {
		log.Printf("[ERROR]: could not download update to directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not download update to directory, reason %v\n", err))
		return
	}

	// Stop service
	if err := openuem_utils.WindowsSvcControl("openuem-agent", svc.Stop, svc.Stopped); err != nil {
		log.Printf("[ERROR]: %v", err)
		msg.NakWithDelay(15 * time.Minute)
		SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("%v", err))
		return
	}

	// Preparing for rollback
	agentPath := filepath.Join(cwd, "openuem-agent.exe")
	agentWasFound := true
	if _, err := os.Stat(agentPath); err != nil {
		log.Printf("[ERROR]: could not find previous OpenUEM Agent, reason %v", err)
		agentWasFound = false
	}

	if agentWasFound {
		// Rename old agent
		rollbackPath := filepath.Join(cwd, "updater", "rollback.exe")
		if err := os.Rename(agentPath, rollbackPath); err != nil {
			log.Printf("[ERROR]: %v", err)
			msg.NakWithDelay(60 * time.Minute)
			SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("%v", err))
			return
		}
	}

	// Rename downloaded agent as the new exe
	if err := os.Rename(downloadPath, agentPath); err != nil {
		log.Printf("[ERROR]: %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("%v", err))
		return
	}

	// Start service
	if err := openuem_utils.WindowsStartService("openuem-agent"); err != nil {
		// We couldn't start service maybe we should rollback
		// but only if we had a previous exe
		if agentWasFound {
			rollbackPath := filepath.Join(cwd, "openuem-agent.exe")
			if err := os.Rename(rollbackPath, agentPath); err != nil {
				log.Printf("[ERROR]: %v", err)
				msg.NakWithDelay(15 * time.Minute)
				SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("%v", err))
				return
			}

			// try to start this exe now
			if err := openuem_utils.WindowsStartService("openuem-agent"); err != nil {
				log.Printf("[FATAL]: %v", err)
				msg.NakWithDelay(15 * time.Minute)
				SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("%v", err))
				return
			}
		} else {
			log.Printf("[FATAL]: %v", err)
			msg.NakWithDelay(15 * time.Minute)
			SaveTaskInfoToRegistry(openuem_nats.UPDATE_ERROR, fmt.Sprintf("%v", err))
			return
		}
	}

	SaveTaskInfoToRegistry(openuem_nats.UPDATE_SUCCESS, "OpenUEM Agent was installed and started")
	log.Println("[INFO]: new OpenUEM Agent was installed and started")

}

func (us *UpdaterService) ReadWindowsConfig() error {
	var err error

	k, err := openuem_utils.OpenRegistryForQuery(registry.LOCAL_MACHINE, `SOFTWARE\OpenUEM\Agent`)
	if err != nil {
		log.Println("[ERROR]: could not open registry")
		return err
	}
	defer k.Close()

	us.NATSServers, err = openuem_utils.GetValueFromRegistry(k, "NATSServers")
	if err != nil {
		return fmt.Errorf("could not read NATS servers from registry")
	}

	us.AgentId, err = openuem_utils.GetValueFromRegistry(k, "UUID")
	if err != nil {
		return fmt.Errorf("could not read NATS servers from registry")
	}

	return nil
}

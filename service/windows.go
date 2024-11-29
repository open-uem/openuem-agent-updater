//go:build windows

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/doncicuto/openuem_nats"
	"github.com/doncicuto/openuem_utils"
	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/mod/semver"
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

	if msg.Subject() == "agent.rollback.messenger" {
		us.rollbackMessengerHandler(msg)
	}

	if msg.Subject() == "agent.update.messenger" {
		us.updateMessengerHandler(msg)
	}

	if msg.Subject() == "agent.rollback.messenger" {
		us.rollbackMessengerHandler(msg)
	}

	if msg.Subject() == "agent.rollback" {
		us.AgentRollback(msg)
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
	s, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "UPDATER_STREAM_" + us.AgentId,
		Subjects: []string{"agentupdate." + us.AgentId, "agent.rollback.messenger." + us.AgentId, "agent.rollback." + us.AgentId},
	})
	if err != nil {
		log.Printf("[ERROR]: could not create stream UPDATER_STREAM: %v\n", err)
		return err
	}
	log.Println("[INFO]: UPDATER_STREAM stream has been created or updated")

	c1, err := s.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
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

	log.Println("[INFO]: Jetstream created and started consuming messages")
	log.Println("[INFO]: subscribed to message ", fmt.Sprintf("agentupdate.%s", us.AgentId))
	log.Println("[INFO]: subscribed to message agent.update.messenger")
	log.Println("[INFO]: subscribed to message agent.rollback.messenger")

	// Subscribe to agent restart
	_, err = us.NATSConnection.QueueSubscribe("agent.restart."+us.AgentId, "openuem-agent-management", us.restartHandler)
	if err != nil {
		log.Printf("[ERROR]: could not subscribe to NATS message, reason: %v", err)
		return err
	}
	log.Printf("[INFO]: subscribed to message agent.restart")

	return nil
}

func (us *UpdaterService) updateMessengerHandler(msg jetstream.Msg) {
	r := openuem_nats.OpenUEMRelease{}

	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Printf("[ERROR]: could get working directory, reason: %v", err)
		return
	}

	if err := json.Unmarshal(msg.Data(), &r); err != nil {
		log.Printf("[ERROR]: could not unmarshal release info, reason: %v", err)
		return
	}

	if r.Version == "" {
		log.Printf("[ERROR]: could not get latest version from update request, reason: %v", err)
		return
	}

	downloadFrom := ""
	downloadHash := ""
	for _, f := range r.Files {
		if f.Arch == runtime.GOARCH && f.Os == runtime.GOOS {
			downloadFrom = f.FileURL
			downloadHash = f.Checksum
			break
		}
	}

	if downloadFrom == "" || downloadHash == "" {
		log.Printf("[ERROR]: could not get applicable download information from release, reason: %v", err)
		return
	}

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\OpenUEM\Agent`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		log.Println("[ERROR]: agent cannot read the agent hive")
		return
	}
	defer k.Close()

	messengerVersion, _, err := k.GetStringValue("MessengerVersion")
	if err != nil {
		log.Println("[ERROR]: agent could not read MessengerVersion entry")
	}

	if semver.Compare("v"+messengerVersion, "v"+r.Version) < 0 {
		downloadPath := filepath.Join(cwd, "updater", "messenger.exe")

		// Download new messenger
		if err := openuem_utils.DownloadFile(downloadFrom, downloadPath, downloadHash); err != nil {
			log.Printf("[ERROR]: could not download new messenger to directory, reason %v\n", err)
			return
		}

		// Preparing for rollback
		messengerPath := filepath.Join(cwd, "openuem-messenger.exe")
		messengerWasFound := true
		if _, err := os.Stat(messengerPath); err != nil {
			log.Printf("[ERROR]: could not find previous OpenUEM messenger, reason: %v\n", err)
			messengerWasFound = false
		}

		if messengerWasFound {
			// Rename old messenger
			rollbackPath := filepath.Join(cwd, "updater", "messenger-rollback.exe")
			if err := os.Rename(messengerPath, rollbackPath); err != nil {
				log.Printf("[ERROR]: could not rename file for rollback, reason: %v", err)
				return
			}
		}

		// Rename messenger as the new exe
		if err := os.Rename(downloadPath, messengerPath); err != nil {
			log.Printf("[ERROR]: could not rename file for replacement, reason: %v", err)
			return
		}

		err := k.SetStringValue("MessengerVersion", r.Version)
		if err != nil {
			log.Println("[ERROR]: agent could not save MessengerVersion entry")
			return
		}

		log.Println("[INFO]: messenger has been updated")
		return
	}
	log.Println("[INFO]: no need to update messenger")
}

func (us *UpdaterService) rollbackMessengerHandler(msg jetstream.Msg) {
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Printf("[ERROR]: could get working directory, reason: %v", err)
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not send ACK for messenger rollback, reason: %v", err)
		}
		return
	}

	// We will keep the messenger version as updated in registry to prevent an update-rollback loop

	// Preparing for rollback
	messengerPath := filepath.Join(cwd, "openuem-messenger.exe")

	rollbackPath := filepath.Join(cwd, "updater", "messenger-rollback.exe")
	rollbackWasFound := true
	if _, err := os.Stat(rollbackPath); err != nil {
		log.Printf("[ERROR]: could not find previous OpenUEM messenger, reason: %v", err)
		rollbackWasFound = false
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not send ACK for messenger rollback, reason: %v", err)
		}
		return
	}

	if rollbackWasFound {
		// Rename old messenger
		rollbackPath := filepath.Join(cwd, "updater", "messenger-rollback.exe")
		if err := os.Rename(rollbackPath, messengerPath); err != nil {
			log.Printf("[ERROR]: could not rename file from rollback, reason: %v", err)
			if err := msg.Ack(); err != nil {
				log.Printf("[ERROR]: could not send ACK for messenger rollback, reason: %v", err)
			}
			return
		}
	}

	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not send ACK for messenger rollback, reason: %v", err)
	}
	log.Println("[INFO]: messenger has been rolled back")
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
	}

	// Preparing for rollback
	agentPath := filepath.Join(cwd, "openuem-agent.exe")
	agentWasFound := true
	if _, err := os.Stat(agentPath); err != nil {
		log.Printf("[WARN]: could not find previous OpenUEM Agent, reason %v", err)
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

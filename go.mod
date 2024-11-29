module github.com/doncicuto/openuem-updater-service

go 1.23.1

replace github.com/doncicuto/openuem_utils => ./internal/utils

replace github.com/doncicuto/openuem_nats => ./internal/nats

require (
	github.com/doncicuto/openuem_nats v0.0.0-00010101000000-000000000000
	github.com/doncicuto/openuem_utils v0.0.0-00010101000000-000000000000
	github.com/go-co-op/gocron/v2 v2.12.3
	github.com/nats-io/nats.go v1.37.0
	golang.org/x/sys v0.26.0
)

require (
	github.com/danieljoos/wincred v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jonboulle/clockwork v0.4.0 // indirect
	github.com/klauspost/compress v1.17.2 // indirect
	github.com/nats-io/nkeys v0.4.7 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	golang.org/x/crypto v0.18.0 // indirect
	golang.org/x/exp v0.0.0-20240613232115-7f521ea00fb8 // indirect
	golang.org/x/mod v0.22.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
)

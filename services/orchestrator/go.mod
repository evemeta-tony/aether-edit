// services/orchestrator/go.mod
module github.com/evemeta-tony/aether-edit/services/orchestrator

go 1.25

// The contracts module is landed by the FT-2 lane at services/contracts;
// this in-repo replace resolves once both branches are merged.
replace github.com/evemeta-tony/aether-edit/services/contracts => ../contracts

require (
	github.com/evemeta-tony/aether-edit/services/contracts v0.0.0
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/nats-io/nats.go v1.46.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/stretchr/testify v1.10.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

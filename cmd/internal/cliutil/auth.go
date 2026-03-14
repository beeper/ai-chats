package cliutil

import "github.com/beeper/agentremote/cmd/internal/beeperauth"

func LoadAuth(path string, missingError func() error) (beeperauth.Config, error) {
	return beeperauth.Load(Store(path, missingError))
}

func ResolveAuth(path string, missingError func() error) (beeperauth.Config, error) {
	return beeperauth.ResolveFromEnvOrStore(Store(path, missingError))
}

func SaveAuth(path string, cfg beeperauth.Config) error {
	return beeperauth.Save(path, cfg)
}

func Store(path string, missingError func() error) beeperauth.Store {
	return beeperauth.Store{
		Path:         path,
		MissingError: missingError,
	}
}

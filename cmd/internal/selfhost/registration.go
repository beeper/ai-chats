package selfhost

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"github.com/beeper/bridge-manager/api/hungryapi"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

type RegistrationParams struct {
	Auth             beeperauth.Config
	SaveAuth         func(beeperauth.Config) error
	ConfigPath       string
	RegistrationPath string
	BeeperBridgeName string
	BridgeType       string
	DBName           string
}

func EnsureRegistration(ctx context.Context, params RegistrationParams) error {
	auth := params.Auth
	who, err := beeperapi.Whoami(auth.Domain, auth.Token)
	if err != nil {
		return fmt.Errorf("whoami failed: %w", err)
	}
	if auth.Username != who.UserInfo.Username {
		auth.Username = who.UserInfo.Username
		if params.SaveAuth != nil {
			if err := params.SaveAuth(auth); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
			}
		}
	}
	hc := hungryapi.NewClient(auth.Domain, auth.Username, auth.Token)
	regCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reg, err := hc.GetAppService(regCtx, params.BeeperBridgeName)
	if err != nil {
		reg, err = hc.RegisterAppService(regCtx, params.BeeperBridgeName, hungryapi.ReqRegisterAppService{Push: false, SelfHosted: true})
		if err != nil {
			return fmt.Errorf("register appservice failed: %w", err)
		}
	}
	yml, err := reg.YAML()
	if err != nil {
		return err
	}
	if err = os.WriteFile(params.RegistrationPath, []byte(yml), 0o600); err != nil {
		return err
	}
	userID := fmt.Sprintf("@%s:%s", auth.Username, auth.Domain)
	if err = bridgeutil.PatchConfigWithRegistration(
		params.ConfigPath,
		&reg,
		hc.HomeserverURL.String(),
		params.BeeperBridgeName,
		params.BridgeType,
		params.DBName,
		auth.Domain,
		reg.AppToken,
		userID,
		auth.Token,
		who.User.AsmuxData.LoginToken,
	); err != nil {
		return err
	}

	state := beeperapi.ReqPostBridgeState{
		StateEvent:   "STARTING",
		Reason:       "SELF_HOST_REGISTERED",
		IsSelfHosted: true,
		BridgeType:   params.BridgeType,
	}
	if err := beeperapi.PostBridgeState(auth.Domain, auth.Username, params.BeeperBridgeName, reg.AppToken, state); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to post bridge state: %v\n", err)
	}
	return nil
}

func DeleteRemoteBridge(ctx context.Context, auth beeperauth.Config, saveAuth func(beeperauth.Config) error, beeperName string) error {
	if auth.Username == "" {
		who, err := beeperapi.Whoami(auth.Domain, auth.Token)
		if err == nil {
			auth.Username = who.UserInfo.Username
			if saveAuth != nil {
				if err := saveAuth(auth); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
				}
			}
		}
	}
	if auth.Username != "" {
		hc := hungryapi.NewClient(auth.Domain, auth.Username, auth.Token)
		deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := hc.DeleteAppService(deleteCtx, beeperName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete appservice: %v\n", err)
		}
	}
	if err := beeperapi.DeleteBridge(auth.Domain, beeperName, auth.Token); err != nil {
		return fmt.Errorf("failed to delete bridge in beeper api: %w", err)
	}
	return nil
}

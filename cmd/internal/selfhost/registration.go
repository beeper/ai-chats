package selfhost

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"github.com/beeper/bridge-manager/api/hungryapi"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

var (
	beeperWhoami    = beeperapi.Whoami
	hungryNewClient = hungryapi.NewClient
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
	who, err := beeperWhoami(auth.Domain, auth.Token)
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
	hc := hungryNewClient(auth.Domain, auth.Username, auth.Token)
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
		who, err := beeperWhoami(auth.Domain, auth.Token)
		if err != nil {
			return fmt.Errorf("failed username discovery for remote bridge deletion: %w", err)
		}
		if who == nil || strings.TrimSpace(who.UserInfo.Username) == "" {
			return fmt.Errorf("failed username discovery for remote bridge deletion: empty username")
		}
		auth.Username = who.UserInfo.Username
		if saveAuth != nil {
			if err := saveAuth(auth); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
			}
		}
	}
	if auth.Username != "" {
		hc := hungryNewClient(auth.Domain, auth.Username, auth.Token)
		deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := hc.DeleteAppService(deleteCtx, beeperName); err != nil && !isRemoteNotFoundError(err) {
			fmt.Fprintf(os.Stderr, "warning: failed to delete appservice: %v\n", err)
		}
	}
	if err := beeperapi.DeleteBridge(auth.Domain, beeperName, auth.Token); err != nil && !isRemoteNotFoundError(err) {
		return fmt.Errorf("failed to delete bridge in beeper api: %w", err)
	}
	fmt.Printf("Waiting for remote bridge %q deletion to complete...\n", beeperName)
	if err := waitForRemoteBridgeDeletion(ctx, auth, beeperName); err != nil {
		return err
	}
	return nil
}

func waitForRemoteBridgeDeletion(ctx context.Context, auth beeperauth.Config, beeperName string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		bridgeGone, appserviceGone, err := remoteBridgeDeleted(waitCtx, auth, beeperName)
		if err != nil {
			return err
		}
		if bridgeGone && appserviceGone {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for remote bridge %q deletion to complete", beeperName)
		case <-ticker.C:
		}
	}
}

func remoteBridgeDeleted(ctx context.Context, auth beeperauth.Config, beeperName string) (bridgeGone bool, appserviceGone bool, err error) {
	bridgeGone = false
	who, err := beeperWhoami(auth.Domain, auth.Token)
	if err != nil {
		return false, false, fmt.Errorf("failed to verify remote bridge deletion: %w", err)
	}
	if who == nil {
		return false, false, fmt.Errorf("unable to verify remote bridge deletion: beeperWhoami returned nil")
	}
	if who.User.Bridges != nil {
		_, exists := who.User.Bridges[beeperName]
		bridgeGone = !exists
	}

	if auth.Username == "" {
		return false, false, fmt.Errorf("failed to verify remote appservice deletion: username unavailable")
	}

	hc := hungryNewClient(auth.Domain, auth.Username, auth.Token)
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = hc.GetAppService(checkCtx, beeperName)
	if err == nil {
		appserviceGone = false
		return bridgeGone, appserviceGone, nil
	}
	if isRemoteNotFoundError(err) {
		appserviceGone = true
		return bridgeGone, appserviceGone, nil
	}
	return false, false, fmt.Errorf("failed to verify remote appservice deletion: %w", err)
}

func isRemoteNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "404") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "m_not_found")
}

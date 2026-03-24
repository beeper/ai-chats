package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	openCodeAPI "github.com/beeper/agentremote/bridges/opencode/api"
)

var (
	_ bridgev2.LoginProcess          = (*OpenCodeLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*OpenCodeLogin)(nil)

	errOpenCodeDefaultPathRequired = agentremote.NewLoginRespError(http.StatusBadRequest, "Enter a default path.", "OPENCODE", "DEFAULT_PATH_REQUIRED")
	errOpenCodeDefaultPathNotDir   = agentremote.NewLoginRespError(http.StatusBadRequest, "Default path must be a directory.", "OPENCODE", "DEFAULT_PATH_NOT_DIRECTORY")
)

const (
	FlowOpenCodeRemote  = "opencode_remote"
	FlowOpenCodeManaged = "opencode_managed"

	openCodeLoginStepRemoteCredentials  = "com.beeper.agentremote.opencode.enter_remote_credentials"
	openCodeLoginStepManagedCredentials = "com.beeper.agentremote.opencode.enter_managed_credentials"
	defaultOpenCodeUsername             = "opencode"
)

type OpenCodeLogin struct {
	agentremote.BaseLoginProcess
	User      *bridgev2.User
	Connector *OpenCodeConnector
	FlowID    string
}

func (ol *OpenCodeLogin) validate() error {
	var br *bridgev2.Bridge
	if ol.Connector != nil {
		br = ol.Connector.br
	}
	return agentremote.ValidateLoginState(ol.User, br)
}

func (ol *OpenCodeLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	switch ol.FlowID {
	case FlowOpenCodeRemote:
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       openCodeLoginStepRemoteCredentials,
			Instructions: "Enter your remote OpenCode server details.",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type:         bridgev2.LoginInputFieldTypeURL,
						ID:           "url",
						Name:         "Server URL",
						Description:  "OpenCode server URL, e.g. http://127.0.0.1:4096",
						DefaultValue: "http://127.0.0.1:4096",
					},
					{
						Type:         bridgev2.LoginInputFieldTypeUsername,
						ID:           "username",
						Name:         "Username",
						Description:  "Optional HTTP basic-auth username.",
						DefaultValue: defaultOpenCodeUsername,
					},
					{
						Type:        bridgev2.LoginInputFieldTypePassword,
						ID:          "password",
						Name:        "Password",
						Description: "Optional HTTP basic-auth password.",
					},
				},
			},
		}, nil
	case FlowOpenCodeManaged:
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       openCodeLoginStepManagedCredentials,
			Instructions: "Enter how the bridge should spawn OpenCode.",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type:         bridgev2.LoginInputFieldTypeUsername,
						ID:           "binary_path",
						Name:         "Binary Path",
						Description:  "Path to the opencode binary the bridge should launch.",
						DefaultValue: defaultManagedOpenCodeBinary(),
					},
					{
						Type:         bridgev2.LoginInputFieldTypeUsername,
						ID:           "default_path",
						Name:         "Default Path",
						Description:  "Default working directory when you leave the path blank in chat.",
						DefaultValue: defaultManagedOpenCodeDirectory(),
					},
				},
			},
		}, nil
	default:
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
}

func (ol *OpenCodeLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}

	var (
		instances  map[string]*OpenCodeInstance
		remoteName string
		instanceID string
		err        error
	)
	switch ol.FlowID {
	case FlowOpenCodeRemote:
		instances, remoteName, instanceID, err = ol.buildRemoteInstances(input)
	case FlowOpenCodeManaged:
		instances, remoteName, instanceID, err = ol.buildManagedInstances(input)
	default:
		err = bridgev2.ErrInvalidLoginFlowID
	}
	if err != nil {
		return nil, err
	}

	for _, existing := range ol.User.GetUserLogins() {
		if existing == nil {
			continue
		}
		existingMeta := loginMetadata(existing)
		if existingMeta.Provider != ProviderOpenCode {
			continue
		}
		if _, ok := existingMeta.OpenCodeInstances[instanceID]; !ok {
			continue
		}
		existingMeta.Provider = ProviderOpenCode
		existingMeta.OpenCodeInstances = instances
		step, err := agentremote.UpdateAndCompleteLogin(
			ctx,
			ol.BackgroundProcessContext(),
			existing,
			remoteName,
			existingMeta,
			"com.beeper.agentremote.opencode.complete",
			ol.Connector.LoadUserLogin,
		)
		if err != nil {
			return nil, agentremote.WrapLoginRespError(fmt.Errorf("failed to update existing login: %w", err), http.StatusInternalServerError, "OPENCODE", "UPDATE_LOGIN_FAILED")
		}
		return step, nil
	}

	_, step, err := agentremote.CreateAndCompleteLogin(
		ctx,
		ol.BackgroundProcessContext(),
		ol.User,
		"opencode",
		remoteName,
		&UserLoginMetadata{
			Provider:          ProviderOpenCode,
			OpenCodeInstances: instances,
		},
		"com.beeper.agentremote.opencode.complete",
		ol.Connector.LoadUserLogin,
	)
	if err != nil {
		return nil, agentremote.WrapLoginRespError(fmt.Errorf("failed to create login: %w", err), http.StatusInternalServerError, "OPENCODE", "CREATE_LOGIN_FAILED")
	}
	return step, nil
}

func (ol *OpenCodeLogin) buildRemoteInstances(input map[string]string) (map[string]*OpenCodeInstance, string, string, error) {
	normalizedURL, err := openCodeAPI.NormalizeBaseURL(input["url"])
	if err != nil {
		return nil, "", "", agentremote.WrapLoginRespError(fmt.Errorf("invalid url: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_URL")
	}
	username := strings.TrimSpace(input["username"])
	if username == "" {
		username = defaultOpenCodeUsername
	}
	password := strings.TrimSpace(input["password"])
	instanceID := OpenCodeInstanceID(normalizedURL, username)
	return map[string]*OpenCodeInstance{
		instanceID: {
			ID:          instanceID,
			Mode:        OpenCodeModeRemote,
			URL:         normalizedURL,
			Username:    username,
			Password:    password,
			HasPassword: password != "",
		},
	}, openCodeRemoteName(normalizedURL, username), instanceID, nil
}

func (ol *OpenCodeLogin) buildManagedInstances(input map[string]string) (map[string]*OpenCodeInstance, string, string, error) {
	binaryPath, err := resolveManagedOpenCodeBinary(input["binary_path"])
	if err != nil {
		return nil, "", "", err
	}
	defaultPath, err := resolveManagedOpenCodeDirectory(input["default_path"])
	if err != nil {
		return nil, "", "", err
	}
	instanceID := OpenCodeManagedLauncherID(string(ol.User.MXID))
	return map[string]*OpenCodeInstance{
		instanceID: {
			ID:               instanceID,
			Mode:             OpenCodeModeManagedLauncher,
			BinaryPath:       binaryPath,
			DefaultDirectory: defaultPath,
		},
	}, openCodeManagedRemoteName(defaultPath), instanceID, nil
}

func openCodeRemoteName(baseURL, username string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return "OpenCode"
	}
	if strings.EqualFold(username, defaultOpenCodeUsername) || username == "" {
		return "OpenCode (" + parsed.Host + ")"
	}
	return fmt.Sprintf("OpenCode (%s@%s)", username, parsed.Host)
}

func openCodeManagedRemoteName(defaultPath string) string {
	defaultPath = strings.TrimSpace(defaultPath)
	if defaultPath == "" {
		return "Managed OpenCode"
	}
	return fmt.Sprintf("Managed OpenCode (%s)", filepath.Base(defaultPath))
}

func defaultManagedOpenCodeBinary() string {
	if path, err := exec.LookPath("opencode"); err == nil {
		return path
	}
	return "opencode"
}

func resolveManagedOpenCodeBinary(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		value = defaultManagedOpenCodeBinary()
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return "", agentremote.WrapLoginRespError(fmt.Errorf("invalid opencode binary path: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_BINARY_PATH")
	}
	return resolved, nil
}

func defaultManagedOpenCodeDirectory() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

func resolveManagedOpenCodeDirectory(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		value = defaultManagedOpenCodeDirectory()
	}
	if value == "" {
		return "", errOpenCodeDefaultPathRequired
	}
	value, err := agentremote.ExpandUserHome(value)
	if err != nil {
		return "", agentremote.WrapLoginRespError(fmt.Errorf("invalid default path: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_DEFAULT_PATH")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", agentremote.WrapLoginRespError(fmt.Errorf("invalid default path: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_DEFAULT_PATH")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", agentremote.WrapLoginRespError(fmt.Errorf("default path is not accessible: %w", err), http.StatusBadRequest, "OPENCODE", "DEFAULT_PATH_NOT_ACCESSIBLE")
	}
	if !info.IsDir() {
		return "", errOpenCodeDefaultPathNotDir
	}
	return abs, nil
}

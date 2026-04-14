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
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	openCodeAPI "github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.LoginProcess          = (*OpenCodeLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*OpenCodeLogin)(nil)

	errOpenCodeDefaultPathRequired = sdk.NewLoginRespError(http.StatusBadRequest, "Enter a default path.", "OPENCODE", "DEFAULT_PATH_REQUIRED")
	errOpenCodeDefaultPathNotDir   = sdk.NewLoginRespError(http.StatusBadRequest, "Default path must be a directory.", "OPENCODE", "DEFAULT_PATH_NOT_DIRECTORY")
)

const (
	FlowOpenCodeRemote  = "opencode_remote"
	FlowOpenCodeManaged = "opencode_managed"

	openCodeLoginStepRemoteCredentials  = "com.beeper.agentremote.opencode.enter_remote_credentials"
	openCodeLoginStepManagedCredentials = "com.beeper.agentremote.opencode.enter_managed_credentials"
	openCodeLoginStepComplete           = "com.beeper.agentremote.opencode.complete"
	defaultOpenCodeUsername             = "opencode"
)

var defaultManagedOpenCodeDirectoryFn = defaultManagedOpenCodeDirectory

type OpenCodeLogin struct {
	sdk.BaseLoginProcess
	User      *bridgev2.User
	Connector *OpenCodeConnector
	FlowID    string
}

func (ol *OpenCodeLogin) validate() error {
	var br *bridgev2.Bridge
	if ol.Connector != nil {
		br = ol.Connector.br
	}
	return sdk.ValidateLoginState(ol.User, br)
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
		err        error
	)
	switch ol.FlowID {
	case FlowOpenCodeRemote:
		instances, remoteName, err = ol.buildRemoteInstances(input)
	case FlowOpenCodeManaged:
		instances, remoteName, err = ol.buildManagedInstances(input)
	default:
		err = bridgev2.ErrInvalidLoginFlowID
	}
	if err != nil {
		return nil, err
	}

	loginID := sdk.NextUserLoginID(ol.User, "opencode")
	instances = ol.scopeInstancesToLogin(loginID, instances)

	_, step, err := sdk.PersistAndCompleteLogin(
		ctx,
		ol.BackgroundProcessContext(),
		ol.User,
		&database.UserLogin{
			ID:         loginID,
			RemoteName: remoteName,
			Metadata: &UserLoginMetadata{
				Provider:          ProviderOpenCode,
				OpenCodeInstances: instances,
			},
		},
		openCodeLoginStepComplete,
		ol.Connector.LoadUserLogin,
		nil,
	)
	if err != nil {
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to create login: %w", err), http.StatusInternalServerError, "OPENCODE", "CREATE_LOGIN_FAILED")
	}
	return step, nil
}

func (ol *OpenCodeLogin) buildRemoteInstances(input map[string]string) (map[string]*OpenCodeInstance, string, error) {
	normalizedURL, err := openCodeAPI.NormalizeBaseURL(input["url"])
	if err != nil {
		return nil, "", sdk.WrapLoginRespError(fmt.Errorf("invalid url: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_URL")
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
	}, openCodeRemoteName(normalizedURL, username), nil
}

func (ol *OpenCodeLogin) buildManagedInstances(input map[string]string) (map[string]*OpenCodeInstance, string, error) {
	binaryPath, err := resolveManagedOpenCodeBinary(input["binary_path"])
	if err != nil {
		return nil, "", err
	}
	defaultPath, err := resolveManagedOpenCodeDirectory(input["default_path"])
	if err != nil {
		return nil, "", err
	}
	instanceID := OpenCodeManagedLauncherID(binaryPath, defaultPath)
	return map[string]*OpenCodeInstance{
		instanceID: {
			ID:               instanceID,
			Mode:             OpenCodeModeManagedLauncher,
			BinaryPath:       binaryPath,
			DefaultDirectory: defaultPath,
		},
	}, openCodeManagedRemoteName(defaultPath), nil
}

func (ol *OpenCodeLogin) scopeInstancesToLogin(loginID networkid.UserLoginID, instances map[string]*OpenCodeInstance) map[string]*OpenCodeInstance {
	if len(instances) == 0 {
		return nil
	}
	scoped := make(map[string]*OpenCodeInstance, len(instances))
	for originalID, inst := range instances {
		if inst == nil {
			continue
		}
		copyInst := *inst
		newID := originalID
		if copyInst.Mode == OpenCodeModeManagedLauncher {
			newID = OpenCodeManagedLauncherID(string(loginID), copyInst.BinaryPath, copyInst.DefaultDirectory)
		}
		copyInst.ID = newID
		scoped[newID] = &copyInst
	}
	return scoped
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
		return "", sdk.WrapLoginRespError(fmt.Errorf("invalid opencode binary path: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_BINARY_PATH")
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
		value = defaultManagedOpenCodeDirectoryFn()
	}
	if value == "" {
		return "", errOpenCodeDefaultPathRequired
	}
	value, err := sdk.ExpandUserHome(value)
	if err != nil {
		return "", sdk.WrapLoginRespError(fmt.Errorf("invalid default path: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_DEFAULT_PATH")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", sdk.WrapLoginRespError(fmt.Errorf("invalid default path: %w", err), http.StatusBadRequest, "OPENCODE", "INVALID_DEFAULT_PATH")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", sdk.WrapLoginRespError(fmt.Errorf("default path is not accessible: %w", err), http.StatusBadRequest, "OPENCODE", "DEFAULT_PATH_NOT_ACCESSIBLE")
	}
	if !info.IsDir() {
		return "", errOpenCodeDefaultPathNotDir
	}
	return abs, nil
}

// Package sshmachine reaches the computers the user adds as SSH machines:
// the agent's files, shell commands and terminal run there, over SSH and
// SFTP, while the agent, its models, Browser Use and Computer Use stay on
// this computer.
package sshmachine

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Settings is an SSH machine as Settings → SSH machines saves it. Its
// password and key passphrase are kept apart (Secrets).
type Settings struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user"`
	Auth     string `json:"auth"`              // "password", "key" or "agent"
	KeyPath  string `json:"keyPath,omitempty"` // auth "key": the private key file on this computer
	Projects string `json:"projects"`          // the folder, on the machine, new projects go in
}

// Secrets are what a machine's sign-in needs and Settings does not keep.
type Secrets struct {
	Password   string `json:"password,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Label is how 1loop names the machine: its name, else user@host.
func (settings Settings) Label() string {
	if strings.TrimSpace(settings.Name) != "" {
		return strings.TrimSpace(settings.Name)
	}
	return settings.User + "@" + settings.Host
}

// Address is host:port, port 22 unless set.
func (settings Settings) Address() string {
	port := settings.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(strings.TrimSpace(settings.Host), strconv.Itoa(port))
}

// Check says what a machine still needs before it can be saved.
func (settings Settings) Check() error {
	switch {
	case strings.TrimSpace(settings.Host) == "":
		return fmt.Errorf("Enter the machine's host name or IP address")
	case strings.ContainsAny(settings.Host, " /@"):
		return fmt.Errorf("Enter the host alone, without user, port or path")
	case settings.Port < 0 || settings.Port > 65535:
		return fmt.Errorf("Enter a port from 1 to 65535")
	case strings.TrimSpace(settings.User) == "":
		return fmt.Errorf("Enter the user name to sign in with")
	case settings.Auth != "password" && settings.Auth != "key" && settings.Auth != "agent":
		return fmt.Errorf("Choose how to sign in")
	case settings.Auth == "key" && strings.TrimSpace(settings.KeyPath) == "":
		return fmt.Errorf("Choose the private key file")
	case strings.TrimSpace(settings.Projects) == "":
		return fmt.Errorf("Enter the folder where projects go on the machine")
	case !strings.HasPrefix(settings.Projects, "/") && settings.Projects != "~" && !strings.HasPrefix(settings.Projects, "~/"):
		return fmt.Errorf("Enter the projects folder as an absolute path, or one starting with ~/")
	}
	return nil
}

// The secrets file sits beside 1loop's settings, readable by this user only.
var secretsMu sync.Mutex

func secretsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "1loop", "ssh-secrets.json"), nil
}

// LoadSecrets reads every machine's secrets, by machine ID.
func LoadSecrets() map[string]Secrets {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	secrets := map[string]Secrets{}
	if path, err := secretsPath(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			json.Unmarshal(data, &secrets)
		}
	}
	return secrets
}

// SaveSecrets keeps one machine's secrets, or forgets them when empty.
func SaveSecrets(id string, secret Secrets) error {
	path, err := secretsPath()
	if err != nil {
		return err
	}
	all := LoadSecrets()
	secretsMu.Lock()
	defer secretsMu.Unlock()
	if secret == (Secrets{}) {
		delete(all, id)
	} else {
		all[id] = secret
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ssh-secrets-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

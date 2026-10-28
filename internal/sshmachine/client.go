package sshmachine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// TrustFunc asks the user whether to trust a host key seen for the first
// time; fingerprint is its SHA256 form, as ssh shows it.
type TrustFunc func(ctx context.Context, machine, address, keyType, fingerprint string) bool

// knownHostsPath is 1loop's own list of trusted host keys. The user's
// ~/.ssh/known_hosts is read too, never written.
func knownHostsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "1loop", "known_hosts"), nil
}

var knownHostsMu sync.Mutex

// hostKeyCallback accepts a key known for this host, asks about an unknown
// one, and refuses a key that differs from the known one.
func hostKeyCallback(ctx context.Context, settings Settings, trust TrustFunc) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()
		own, err := knownHostsPath()
		if err != nil {
			return err
		}
		var files []string
		if home, err := os.UserHomeDir(); err == nil {
			if path := filepath.Join(home, ".ssh", "known_hosts"); fileExists(path) {
				files = append(files, path)
			}
		}
		if fileExists(own) {
			files = append(files, own)
		}
		if len(files) > 0 {
			check, err := knownhosts.New(files...)
			if err != nil {
				return fmt.Errorf("The known hosts could not be read: %w", err)
			}
			err = check(hostname, remote, key)
			var keyErr *knownhosts.KeyError
			switch {
			case err == nil:
				return nil
			case errors.As(err, &keyErr) && len(keyErr.Want) > 0:
				return fmt.Errorf("The host key of %s changed (now %s %s). This can mean someone is intercepting the connection; 1loop refuses it. If the machine was reinstalled, remove its old key from %s",
					hostname, key.Type(), ssh.FingerprintSHA256(key), keyErr.Want[0].Filename)
			case !errors.As(err, &keyErr):
				return err
			}
		}
		if trust == nil || !trust(ctx, settings.Label(), hostname, key.Type(), ssh.FingerprintSHA256(key)) {
			return fmt.Errorf("The host key of %s was not trusted", hostname)
		}
		if err := os.MkdirAll(filepath.Dir(own), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(own, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		_, err = file.WriteString(knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n")
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		return err
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// expandHome reads ~/ as this computer's home folder.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[min(len(path), 2):])
		}
	}
	return path
}

// authMethods are the sign-in ways settings chose, with their secrets.
func authMethods(settings Settings, secrets Secrets) ([]ssh.AuthMethod, func(), error) {
	switch settings.Auth {
	case "password":
		if secrets.Password == "" {
			return nil, nil, fmt.Errorf("Enter the password of %s in Settings → SSH machines", settings.Label())
		}
		// Servers that only offer keyboard-interactive ask for the same password.
		answer := func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = secrets.Password
			}
			return answers, nil
		}
		return []ssh.AuthMethod{ssh.Password(secrets.Password), ssh.KeyboardInteractive(answer)}, func() {}, nil
	case "key":
		data, err := os.ReadFile(expandHome(strings.TrimSpace(settings.KeyPath)))
		if err != nil {
			return nil, nil, fmt.Errorf("The private key could not be read: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(data)
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			if secrets.Passphrase == "" {
				return nil, nil, fmt.Errorf("The private key of %s is protected: enter its passphrase in Settings → SSH machines", settings.Label())
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(secrets.Passphrase))
		}
		if err != nil {
			return nil, nil, fmt.Errorf("The private key could not be used: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, func() {}, nil
	case "agent":
		socket := os.Getenv("SSH_AUTH_SOCK")
		if runtime.GOOS == "windows" || socket == "" {
			return nil, nil, fmt.Errorf("No SSH agent is available to 1loop (SSH_AUTH_SOCK is not set). Use a key file or a password")
		}
		connection, err := net.Dial("unix", socket)
		if err != nil {
			return nil, nil, fmt.Errorf("The SSH agent could not be reached: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(connection).Signers)}, func() { connection.Close() }, nil
	}
	return nil, nil, fmt.Errorf("Choose how to sign in to %s", settings.Label())
}

// dial opens one SSH connection to the machine.
func dial(ctx context.Context, settings Settings, secrets Secrets, trust TrustFunc) (*ssh.Client, error) {
	methods, done, err := authMethods(settings, secrets)
	if err != nil {
		return nil, err
	}
	defer done()
	config := &ssh.ClientConfig{
		User:            settings.User,
		Auth:            methods,
		HostKeyCallback: hostKeyCallback(ctx, settings, trust),
		Timeout:         20 * time.Second,
	}
	address := settings.Address()
	connection, err := (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached: %w", address, err)
	}
	// The handshake may wait for the user to trust the host key.
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	conn, channels, requests, err := ssh.NewClientConn(connection, address, config)
	if err != nil {
		connection.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A server refusing keyboard-interactive outright, after a wrong
		// password, ends x/crypto's attempt with "unexpected message type 51".
		if strings.Contains(err.Error(), "unable to authenticate") || strings.Contains(err.Error(), "unexpected message type 51") {
			return nil, fmt.Errorf("%s refused the sign-in of %s: check the user name and the %s", address, settings.User, map[string]string{"password": "password", "key": "key", "agent": "SSH agent's keys"}[settings.Auth])
		}
		return nil, err
	}
	return ssh.NewClient(conn, channels, requests), nil
}

// connection is one SSH connection and its SFTP client.
type connection struct {
	client *ssh.Client
	files  *sftp.Client
	broken chan struct{}
}

func (conn *connection) alive() bool {
	select {
	case <-conn.broken:
		return false
	default:
		return true
	}
}

func (conn *connection) close() {
	if conn.files != nil {
		conn.files.Close()
	}
	conn.client.Close()
}

// keepAlive notices a connection the network dropped, so the next use
// connects again instead of waiting on a dead one.
func (conn *connection) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-conn.broken:
			return
		case <-ticker.C:
			reply := make(chan error, 1)
			go func() {
				_, _, err := conn.client.SendRequest("keepalive@openssh.com", true, nil)
				reply <- err
			}()
			select {
			case err := <-reply:
				if err != nil {
					conn.client.Close()
					return
				}
			case <-time.After(15 * time.Second):
				conn.client.Close()
				return
			}
		}
	}
}

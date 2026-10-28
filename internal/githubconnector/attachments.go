package githubconnector

// Images and videos for issues, pull requests and comments go to GitHub's
// attachment endpoint, the one its web editor and `gh --attach` use. It
// refuses GitHub App tokens (only GitHub's own apps may use it), so uploads
// go out with a personal access token saved by the user, or else with
// GitHub CLI's sign-in, read when needed and never saved.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"1loop/internal/platform"
)

var uploadURL = "https://uploads.github.com"

// GitHub's own limits for attachments, as GitHub CLI applies them.
const (
	MaxImageBytes = 10 << 20
	MaxVideoBytes = 100 << 20
)

var attachmentTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".svg": "image/svg+xml",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
}

// AttachmentType is the content type GitHub expects for a file name and the
// largest size it accepts.
func AttachmentType(name string) (string, int64, error) {
	contentType, ok := attachmentTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return "", 0, fmt.Errorf("%s: GitHub attaches only png, jpg, jpeg, gif, webp, svg, mp4, mov and webm files", filepath.Base(name))
	}
	if strings.HasPrefix(contentType, "video/") {
		return contentType, MaxVideoBytes, nil
	}
	return contentType, MaxImageBytes, nil
}

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// SetAttachmentToken saves the personal access token used for attachments
// after GitHub confirms its account; an empty token removes it.
func (manager *Manager) SetAttachmentToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	login := ""
	if token != "" {
		if strings.HasPrefix(token, "ghu_") || strings.HasPrefix(token, "ghs_") {
			return fmt.Errorf("GitHub refuses GitHub App tokens for attachments. Use a personal access token")
		}
		var err error
		if login, err = manager.user(ctx, token); err != nil {
			return fmt.Errorf("GitHub did not accept this token")
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err != nil {
		return err
	}
	saved.AttachmentToken, saved.AttachmentLogin = token, login
	return manager.save(saved)
}

// cliToken is GitHub CLI's sign-in for github.com, if gh is installed and
// signed in.
var cliToken = func(ctx context.Context) (string, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "auth", "token", "--hostname", "github.com")
	platform.PrepareProcess(command)
	out, err := command.Output()
	token := strings.TrimSpace(string(out))
	if err != nil || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("GitHub CLI is not signed in")
	}
	return token, nil
}

func cliInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// attachmentToken is the credential for the attachment endpoint and where
// it comes from.
func (manager *Manager) attachmentToken(ctx context.Context) (token, login, source string, err error) {
	manager.mu.Lock()
	saved, err := manager.load()
	manager.mu.Unlock()
	if err != nil {
		return "", "", "", err
	}
	if saved.AttachmentToken != "" {
		return saved.AttachmentToken, saved.AttachmentLogin, "personal access token", nil
	}
	if token, err = cliToken(ctx); err != nil {
		return "", "", "", fmt.Errorf("GitHub refuses the connector's app sign-in for attachments. Add a personal access token in Settings → Connectors → GitHub, or sign in to GitHub CLI on this execution target (gh auth login)")
	}
	if login, err = manager.user(ctx, token); err != nil {
		return "", "", "", fmt.Errorf("GitHub did not accept GitHub CLI's sign-in. Run gh auth login again")
	}
	return token, login, "GitHub CLI", nil
}

// AttachmentTarget is a repository ready to receive attachments.
type AttachmentTarget struct {
	Repository string // owner/name as GitHub spells it
	Login      string // the account that uploads
	Source     string // where its credential comes from
	id         int64
	token      string
	manager    *Manager
}

// Attachments checks that the uploading account can write to the
// repository: GitHub accepts attachments only from such accounts.
func (manager *Manager) Attachments(ctx context.Context, repository string) (*AttachmentTarget, error) {
	repository = strings.TrimSpace(repository)
	if !repositoryName.MatchString(repository) {
		return nil, fmt.Errorf("Name the repository as owner/name")
	}
	token, login, source, err := manager.attachmentToken(ctx)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", apiURL+"/repos/"+repository, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := manager.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	var repo struct {
		ID          int64  `json:"id"`
		FullName    string `json:"full_name"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&repo) != nil || repo.ID == 0 {
		return nil, fmt.Errorf("@%s (%s) cannot see %s on GitHub", login, source, repository)
	}
	if !repo.Permissions.Push {
		return nil, fmt.Errorf("@%s (%s) cannot write to %s. GitHub accepts attachments only from accounts that can write to the repository", login, source, repo.FullName)
	}
	return &AttachmentTarget{Repository: repo.FullName, Login: login, Source: source, id: repo.ID, token: token, manager: manager}, nil
}

// Upload sends one file and returns its address on GitHub. An upload cannot
// be deleted; it shows wherever its address is referenced.
func (target *AttachmentTarget) Upload(ctx context.Context, name, contentType string, data []byte) (string, error) {
	query := url.Values{"name": {name}, "content_type": {contentType}, "repository_id": {strconv.FormatInt(target.id, 10)}}
	request, err := http.NewRequestWithContext(ctx, "POST", uploadURL+"/user-attachments/assets?"+query.Encode(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "token "+target.token)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/vnd.github+json")
	client := *target.manager.client
	client.Timeout = 5 * time.Minute // videos reach 100 MiB
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var reply struct {
		URL     string `json:"url"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &reply)
	switch {
	case response.StatusCode/100 == 2 && strings.HasPrefix(reply.URL, "https://github.com/"):
		return reply.URL, nil
	case response.StatusCode == http.StatusUnauthorized:
		return "", fmt.Errorf("GitHub rejected the %s of @%s. Replace it", target.Source, target.Login)
	case response.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("GitHub refused the upload: @%s needs write access to %s", target.Login, target.Repository)
	case response.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("GitHub limits uploads right now. Retry after %s seconds", response.Header.Get("Retry-After"))
	case reply.Message != "":
		return "", fmt.Errorf("GitHub refused the upload (HTTP %d): %s", response.StatusCode, reply.Message)
	}
	return "", fmt.Errorf("GitHub refused the upload (HTTP %d)", response.StatusCode)
}

// attachmentAddress tells whether an address is an attachment of GitHub and
// whether a credential may go with it (only to github.com itself).
func attachmentAddress(address *url.URL) (ok, signed bool) {
	if address.Scheme != "https" || address.User != nil || address.Port() != "" {
		return false, false
	}
	host := strings.ToLower(address.Hostname())
	switch {
	case host == "github.com":
		parts := strings.Split(strings.Trim(address.Path, "/"), "/")
		attachment := len(parts) == 3 && parts[0] == "user-attachments" && (parts[1] == "assets" || parts[1] == "files") ||
			len(parts) == 5 && parts[2] == "assets"
		return attachment, attachment
	case host == "user-images.githubusercontent.com", host == "private-user-images.githubusercontent.com":
		return true, false
	}
	return false, false
}

// storageAddress is where github.com redirects an attachment: GitHub's own
// storage, reached without a credential.
func storageAddress(address *url.URL) bool {
	host := strings.ToLower(address.Hostname())
	return address.Scheme == "https" && (strings.HasSuffix(host, ".githubusercontent.com") ||
		strings.HasPrefix(host, "github-production-") && strings.HasSuffix(host, ".s3.amazonaws.com"))
}

// Download reads an image or video attached to an issue, pull request or
// comment. github.com gets the attachment credential, else the connector's,
// so attachments of private repositories open too.
func (manager *Manager) Download(ctx context.Context, address string, limit int64) ([]byte, string, error) {
	current, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return nil, "", fmt.Errorf("Give the address of a GitHub attachment")
	}
	ok, signed := attachmentAddress(current)
	if !ok {
		return nil, "", fmt.Errorf("Only GitHub attachment addresses open here (https://github.com/user-attachments/assets/…)")
	}
	authorization := ""
	if signed {
		if token, _, _, err := manager.attachmentToken(ctx); err == nil {
			authorization = "token " + token
		} else if server, err := manager.Server(ctx); err == nil {
			authorization = server.Headers["Authorization"]
		}
	}
	for redirects := 0; ; redirects++ {
		request, err := http.NewRequestWithContext(ctx, "GET", current.String(), nil)
		if err != nil {
			return nil, "", err
		}
		if authorization != "" && strings.EqualFold(current.Hostname(), "github.com") {
			request.Header.Set("Authorization", authorization)
		}
		response, err := manager.client.Do(request)
		if err != nil {
			return nil, "", fmt.Errorf("Could not reach GitHub: %w", err)
		}
		if response.StatusCode/100 == 3 {
			response.Body.Close()
			next, err := current.Parse(response.Header.Get("Location"))
			if err != nil || redirects == 3 {
				return nil, "", fmt.Errorf("GitHub redirected the attachment too often")
			}
			if ok, _ := attachmentAddress(next); !ok && !storageAddress(next) {
				return nil, "", fmt.Errorf("GitHub redirected the attachment outside its storage")
			}
			current = next
			continue
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("GitHub did not serve this attachment (HTTP %d). Its repository may be private to another account", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err != nil {
			return nil, "", err
		}
		if int64(len(data)) > limit {
			return nil, "", errors.New("This attachment is too large to open")
		}
		contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = http.DetectContentType(data)
		}
		return data, contentType, nil
	}
}

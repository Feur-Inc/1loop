// SPDX-License-Identifier: MPL-2.0

package ui

// Each paired server's projects and chats stay in the sidebar while the
// window works on another target (#359, #383): a link per server reads
// them every few seconds, which also says whether it answers. While the
// window works on a server, its own connection does it (remote_desktop.go),
// and that server's link waits.

import (
	"context"
	"maps"
	"slices"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/remoteapi"
)

const (
	linkEvery = 5 * time.Second  // between two reads, while the server answers
	linkRetry = 15 * time.Second // between two tries, while it does not
)

// serverLink follows a paired server for the sidebar.
type serverLink struct {
	address  serverAddress
	cancel   context.CancelFunc
	wake     chan struct{}
	answered bool // it answered since the link started, or the window's connection did
	listing  serverListing
}

// linkServers follows every paired server, in the order they were paired,
// and stops following one that is no longer paired, or no longer the same
// way: called at start and when the window takes a server.
func (ui *desktopUI) linkServers() {
	if browserClient { // the page's server is the window's only target
		return
	}
	var links []*serverLink
	for _, address := range loadServerAddresses() {
		if i := slices.IndexFunc(ui.links, func(link *serverLink) bool { return link.address == address }); i >= 0 {
			links = append(links, ui.links[i])
			continue
		}
		client, err := newRemoteClient(address)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithCancel(ui.ctx)
		link := &serverLink{address: address, cancel: cancel, wake: make(chan struct{}, 1)}
		links = append(links, link)
		go ui.followServer(ctx, link, client)
	}
	for _, link := range ui.links {
		if !slices.Contains(links, link) {
			link.cancel()
		}
	}
	ui.links = links
}

// linkTo is the link that follows the server at an address, nil if none.
func (ui *desktopUI) linkTo(address string) *serverLink {
	for _, link := range ui.links {
		if link.address.URL == address {
			return link
		}
	}
	return nil
}

// followServer reads the server's projects and chats until the link ends,
// but not while the window works on the server.
func (ui *desktopUI) followServer(ctx context.Context, link *serverLink, client *remoteClient) {
	target := serverTarget(link.address.URL)
	for ctx.Err() == nil {
		var inUse bool
		fyne.DoAndWait(func() { inUse = ui.server != nil && ui.server.client.address.URL == link.address.URL })
		wait := linkEvery
		if !inUse {
			listing, err := readServerListing(ctx, client)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				wait = linkRetry
			}
			fyne.Do(func() {
				if !slices.Contains(ui.links, link) || ui.server != nil && ui.server.client.address.URL == link.address.URL {
					return
				}
				if err != nil {
					ui.setReach(target, reachOffline, err.Error())
					return
				}
				changed := !link.answered || !listing.equal(link.listing)
				link.answered, link.listing = true, listing
				ui.rememberServerProjects(link.address.URL, listing.projects)
				if !ui.setReach(target, reachOnline, "") && changed {
					ui.renderChats()
				}
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-link.wake:
		case <-time.After(wait):
		}
	}
}

// readServerListing reads a server's name, projects and chats.
func readServerListing(ctx context.Context, client *remoteClient) (serverListing, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var state struct {
		Name, Workspace string
		Chats           []remoteapi.Chat
		Worktrees       map[string]string
	}
	if err := client.json(ctx, "GET", "/api/state", nil, &state); err != nil {
		return serverListing{}, err
	}
	var listed struct{ Projects []string }
	client.json(ctx, "GET", "/api/projects", nil, &listed) // an older server has none to list
	return serverListing{name: state.Name, chats: state.Chats, worktrees: state.Worktrees,
		projects: serverProjectList(state.Workspace, listed.Projects, state.Chats, state.Worktrees)}, nil
}

func (listing serverListing) equal(other serverListing) bool {
	return listing.name == other.name && slices.Equal(listing.projects, other.projects) &&
		slices.Equal(listing.chats, other.chats) && maps.Equal(listing.worktrees, other.worktrees)
}

// handOver gives the server's link what the window's connection to it
// last listed, as the window leaves the server: its projects and chats
// stay listed as they were until the link reads them again, at once.
func (ui *desktopUI) handOver(remote *remoteDesktop) {
	link := ui.linkTo(remote.client.address.URL)
	if link == nil || remote.name == "" {
		return
	}
	link.answered = true
	link.listing = serverListing{name: remote.name, projects: remote.projects, chats: remote.chats, worktrees: remote.worktrees}
	select {
	case link.wake <- struct{}{}:
	default:
	}
}

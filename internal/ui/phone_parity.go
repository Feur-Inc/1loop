// SPDX-License-Identifier: MPL-2.0

package ui

// Routes a headless server had first, served by the desktop's remote too
// so that the phone sees the same API from both (#518). Those that stay a
// server's alone are listed in remoteapi.ServerOnlyRoutes.

import (
	"net/http"
	"slices"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
)

// pairedDevices is GET /api/devices: the phones paired with this computer,
// the one asking marked current (#428).
func (server *remoteServer) pairedDevices(w http.ResponseWriter, r *http.Request) {
	hash := remoteapi.RequestTokenHash(r)
	devices := []map[string]any{}
	server.onUI(func() {
		for _, device := range server.ui.devices {
			devices = append(devices, map[string]any{"id": remoteapi.DeviceID(device), "name": device.Name, "added": device.Added, "current": device.TokenHash == hash})
		}
	})
	core.WriteJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// revokePairedDevice is POST /api/devices/{id}/revoke: a phone unpairs
// another one, or itself when it forgets this computer (#428), as Forget
// does in Settings → Phone.
func (server *remoteServer) revokePairedDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	server.onUI(func() {
		index := slices.IndexFunc(server.ui.devices, func(device remoteapi.PairedDevice) bool { return id != "" && remoteapi.DeviceID(device) == id })
		if index < 0 {
			return
		}
		found = true
		server.ui.setDevices(slices.Delete(slices.Clone(server.ui.devices), index, index+1))
		server.ui.save()
		if server.ui.prefs != nil && server.ui.prefs.on("Phone") {
			server.ui.prefs.show("Phone")
		}
	})
	if !found {
		core.Fail(w, http.StatusNotFound, "This device is not paired")
		return
	}
	server.disconnectAll() // the others reconnect with their token
	core.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// readProjectEnv is GET /api/projects/env?path=: a project's environment
// variables (#408).
func (server *remoteServer) readProjectEnv(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	known := false
	var env map[string]string
	server.onUI(func() {
		if known = slices.Contains(server.ui.projects, path); known {
			env = server.ui.projectEnvOf(localTarget, path)
		}
	})
	if !known {
		core.Fail(w, http.StatusBadRequest, "Choose a registered project")
		return
	}
	if env == nil {
		env = map[string]string{}
	}
	core.WriteJSON(w, http.StatusOK, remoteapi.ProjectEnv{Path: path, Env: env})
}

// saveProjectEnv is POST /api/projects/env with {path, env}: env replaces
// the project's variables; an empty one forgets them. As on a server, the
// answer does not repeat them.
func (server *remoteServer) saveProjectEnv(w http.ResponseWriter, r *http.Request) {
	var args remoteapi.ProjectEnv
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, http.StatusBadRequest, "Send the project and its variables")
		return
	}
	for name := range args.Env {
		if !platform.ValidEnvName(name) {
			core.Fail(w, http.StatusBadRequest, "Not a variable name: "+name)
			return
		}
	}
	known := false
	server.onUI(func() {
		if known = slices.Contains(server.ui.projects, args.Path); known {
			server.ui.keepProjectEnv(localTarget, args.Path, args.Env)
		}
	})
	if !known {
		core.Fail(w, http.StatusBadRequest, "Choose a registered project")
		return
	}
	core.WriteJSON(w, http.StatusOK, remoteapi.ProjectEnv{Path: args.Path, Env: map[string]string{}})
}

// setNamingModel is POST /api/naming-model {model}: the model that names
// new chats, null for none, as in Models & accounts.
func (server *remoteServer) setNamingModel(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Model *agent.NamingModel `json:"model"`
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if !args.Model.Set() {
		args.Model = nil
	}
	available := true
	server.onUI(func() {
		if args.Model != nil {
			available = slices.ContainsFunc(server.ui.namingOptions(), func(option namingOption) bool { return *option.model == *args.Model })
		}
		if !available {
			return
		}
		server.ui.namingModel = args.Model
		server.ui.save()
		server.settingsChanged()
	})
	if !available {
		core.Fail(w, http.StatusBadRequest, "This model is unavailable")
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"namingModel": args.Model})
}

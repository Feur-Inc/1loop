package gitrepo

import (
	"context"
	"errors"
	"net/http"
	"time"

	"1loop/internal/core"
)

// Target is an execution target serving its projects' repositories.
type Target interface {
	// Project is a registered project of the target, checked.
	Project(path string) (string, error)
	// Opened and Removed keep the target's list of worktrees: their
	// chats belong to the project.
	Opened(project, worktree string)
	Removed(project, worktree string)
}

// Handlers are the repository routes, behind the target's existing
// authorization like its other project routes.
func Handlers(target Target, authorizations func(context.Context) []string) map[string]http.HandlerFunc {
	project := func(w http.ResponseWriter, path string) (string, bool) {
		checked, err := target.Project(path)
		if err != nil {
			core.Fail(w, http.StatusBadRequest, err.Error())
			return "", false
		}
		return checked, true
	}
	open := func(w http.ResponseWriter, r *http.Request, path string) (*Repository, bool) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		repo, err := Open(ctx, Local, path)
		if errors.Is(err, ErrNotRepository) {
			core.Fail(w, http.StatusNotFound, err.Error())
			return nil, false
		}
		if err != nil {
			core.Fail(w, http.StatusBadRequest, err.Error())
			return nil, false
		}
		for _, worktree := range repo.Worktrees {
			if !worktree.Main {
				target.Opened(path, worktree.Path)
			}
		}
		return repo, true
	}
	return map[string]http.HandlerFunc{
		"GET /api/git": func(w http.ResponseWriter, r *http.Request) {
			path, ok := project(w, r.URL.Query().Get("project"))
			if !ok {
				return
			}
			if repo, ok := open(w, r, path); ok {
				core.WriteJSON(w, http.StatusOK, repo)
			}
		},
		"GET /api/git/github": func(w http.ResponseWriter, r *http.Request) {
			path, ok := project(w, r.URL.Query().Get("project"))
			if !ok {
				return
			}
			repo, ok := open(w, r, path)
			if !ok {
				return
			}
			if repo.Owner == "" {
				core.Fail(w, http.StatusNotFound, "This repository has no GitHub remote named origin")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
			defer cancel()
			hub, err := GitHub(ctx, repo.Owner, repo.Name, authorizations(ctx))
			if err != nil {
				core.Fail(w, http.StatusBadGateway, err.Error())
				return
			}
			repo.Link(hub)
			pulls := map[string]int{}
			for _, worktree := range repo.Worktrees {
				if worktree.Pull != nil {
					pulls[worktree.Path] = worktree.Pull.Number
				}
			}
			core.WriteJSON(w, http.StatusOK, map[string]any{"hub": hub, "worktreePulls": pulls})
		},
		"POST /api/git/worktrees": func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Project string
				NewWorktree
			}
			if core.ReadJSON(r, &body) != nil {
				core.Fail(w, http.StatusBadRequest, "Invalid worktree")
				return
			}
			path, ok := project(w, body.Project)
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			defer cancel()
			created, err := AddWorktree(ctx, Local, path, body.NewWorktree)
			if err != nil {
				core.Fail(w, http.StatusBadRequest, err.Error())
				return
			}
			target.Opened(path, created.Path)
			core.WriteJSON(w, http.StatusOK, created)
		},
		"POST /api/git/worktrees/remove": func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Project string
				Removal
			}
			if core.ReadJSON(r, &body) != nil {
				core.Fail(w, http.StatusBadRequest, "Invalid worktree")
				return
			}
			path, ok := project(w, body.Project)
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			note, err := RemoveWorktree(ctx, Local, path, body.Removal)
			if err != nil {
				core.Fail(w, http.StatusBadRequest, err.Error())
				return
			}
			target.Removed(path, body.Path)
			core.WriteJSON(w, http.StatusOK, map[string]string{"note": note})
		},
	}
}

// SPDX-License-Identifier: MPL-2.0

package usage

// The Usage page's routes, the same on a server, on the phone's own engine
// and on the desktop serving its phone: the report, and the prices behind
// it.

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"1loop/internal/core"
)

// QueryOf reads a query from a request's parameters.
func QueryOf(r *http.Request) Query {
	values := r.URL.Query()
	query := Query{Period: values.Get("period"), Group: values.Get("group"), Chat: values.Get("chat")}
	query.Offset, _ = strconv.Atoi(values.Get("offset"))
	if text := values.Get("compare"); text != "" {
		if offset, err := strconv.Atoi(text); err == nil {
			query.Compare = &offset
		}
	}
	return query
}

// Encode writes a query as request parameters.
func (query Query) Encode() string {
	values := url.Values{}
	for key, value := range map[string]string{"period": query.Period, "group": query.Group, "chat": query.Chat} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if query.Offset != 0 {
		values.Set("offset", strconv.Itoa(query.Offset))
	}
	if query.Compare != nil {
		values.Set("compare", strconv.Itoa(*query.Compare))
	}
	return values.Encode()
}

// Models are the models the ledger has seen.
func (ledger *Ledger) Models() []string {
	seen := map[string]bool{}
	var models []string
	for _, record := range ledger.Between(time.Time{}, time.Time{}) {
		if key := Key(record.Model); key != "" && !seen[key] {
			seen[key] = true
			models = append(models, key)
		}
	}
	return models
}

// Handlers serves the ledger. names gives chats and connections their
// names; changed is told when a price changes.
func Handlers(ledger *Ledger, prices *Prices, names Names, changed func()) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/usage": func(w http.ResponseWriter, r *http.Request) {
			core.WriteJSON(w, 200, Build(ledger, prices, names, QueryOf(r), time.Now()))
		},
		"GET /api/usage/prices": func(w http.ResponseWriter, r *http.Request) {
			core.WriteJSON(w, 200, prices.Table(ledger.Models()))
		},
		"POST /api/usage/prices": func(w http.ResponseWriter, r *http.Request) {
			var change struct {
				Model string `json:"model"`
				Price *Price `json:"price"`
			}
			if core.ReadJSON(r, &change) != nil || Key(change.Model) == "" {
				core.Fail(w, 400, "Name a model")
				return
			}
			if err := prices.Set(change.Model, change.Price); err != nil {
				core.Fail(w, 500, err.Error())
				return
			}
			if changed != nil {
				changed()
			}
			core.WriteJSON(w, 200, prices.Table(ledger.Models()))
		},
	}
}

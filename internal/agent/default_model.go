package agent

import "fmt"

// DefaultModelChoices replaces the provider's Default alias only when the
// account has a preset. Targets come from this account's unfiltered catalog.
func DefaultModelChoices(conn Connection, catalog []ModelChoice) []ModelChoice {
	if len(conn.DefaultModels) == 0 {
		return catalog
	}
	preset := ModelChoice{Connection: conn, ID: "default", Name: "Default"}
	var choices []ModelChoice
	for _, choice := range catalog {
		if choice.ID != "default" {
			choices = append(choices, choice)
			preset.DefaultTargets = append(preset.DefaultTargets, choice)
		}
	}
	for _, effort := range EffortOrder {
		if conn.DefaultModels[effort] != "" {
			preset.Efforts = append(preset.Efforts, effort)
		}
	}
	if len(preset.Efforts) == 0 {
		return choices
	}
	preset.DefaultEffort = preset.Efforts[0]
	if Contains(preset.Efforts, "medium") {
		preset.DefaultEffort = "medium"
	}
	return append(choices, preset)
}

func (conn *Connection) SetDefaultModels(models map[string]string, catalog []ModelChoice) error {
	copy := make(map[string]string, len(models))
	for effort, id := range models {
		if !Contains(EffortOrder, effort) || id == "" || id == "default" {
			return fmt.Errorf("Choose a model and a supported preset level")
		}
		found := false
		for _, choice := range catalog {
			found = found || choice.Connection.ID == conn.ID && choice.ID == id
		}
		if !found {
			return fmt.Errorf("Model %q is not available in this account", id)
		}
		copy[effort] = id
	}
	conn.DefaultModels = copy
	return nil
}

func (choice ModelChoice) IsDefaultPreset() bool {
	return choice.ID == "default" && len(choice.Connection.DefaultModels) > 0
}

func (choice ModelChoice) DefaultTarget(effort string) (ModelChoice, bool) {
	id := choice.Connection.DefaultModels[effort]
	for _, target := range choice.DefaultTargets {
		if target.ID == id {
			return target, true
		}
	}
	return ModelChoice{}, false
}

func targetEffort(choice ModelChoice, wanted string) string {
	if Contains(choice.Efforts, wanted) {
		return wanted
	}
	return choice.DefaultEffort
}

func (choice ModelChoice) DefaultAbout(effort string) string {
	if !choice.IsDefaultPreset() {
		return ""
	}
	target, ok := choice.DefaultTarget(effort)
	if !ok {
		return "Model unavailable · update this account's Default preset"
	}
	actual := targetEffort(target, effort)
	if actual == "" {
		return target.Name + " · no reasoning control"
	}
	return target.Name + " · " + EffortLabel(actual)
}

// ResolveDefault changes only a run's copy. The saved selection stays Default;
// handoffs, context limits and provider requests use the actual model identity.
func ResolveDefault(cfg Config, conn Connection, choices map[string]ModelChoice) Config {
	if cfg.Model != "default" {
		return cfg
	}
	if len(conn.DefaultModels) == 0 {
		if cfg.DefaultPreset {
			cfg.selectionError = fmt.Errorf("This account’s Default preset was disabled. Choose a model")
		}
		return cfg
	}
	for _, choice := range choices {
		if choice.Connection.ID != conn.ID || choice.ID != "default" {
			continue
		}
		choice.Connection = conn
		effort := cfg.Effort
		if conn.DefaultModels[effort] == "" {
			effort = "medium"
			if conn.DefaultModels[effort] == "" {
				for _, level := range EffortOrder {
					if conn.DefaultModels[level] != "" {
						effort = level
						break
					}
				}
			}
		}
		if target, ok := choice.DefaultTarget(effort); ok {
			selected := cfg
			cfg.presetSelection = &selected
			cfg.Model = target.ID
			cfg.Effort = targetEffort(target, effort)
			cfg.ReasoningMode = target.ReasoningMode
			cfg.ServiceTier = TierFor(target, cfg.ServiceTier)
			options := conn.ModelOptions[target.ID]
			cfg.Routing = MergeOptions(conn.Routing, options.Provider)
			cfg.Parameters = MergeOptions(conn.Parameters, options.Parameters)
			cfg.CompactAt = options.CompactAt
			return cfg
		}
	}
	cfg.selectionError = fmt.Errorf("The Default preset model is unavailable. Update this account's Default preset in Settings")
	return cfg
}

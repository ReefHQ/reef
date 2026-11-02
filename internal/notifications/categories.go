package notifications

import (
	"errors"
	"slices"
)

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func CategoryOptions() []Category {
	return []Category{
		{"update", "Updates available"}, {"container", "Containers"}, {"project", "Projects"}, {"image", "Images"},
		{"volume", "Volumes"}, {"network", "Networks"}, {"build", "Builds"},
		{"agent", "Agents"}, {"console", "Consoles"}, {"schedule", "Schedules"},
		{"monitor", "Alerts & monitoring"}, {"webhook", "Webhook settings"},
	}
}

// nil means all (including legacy state); a non-nil empty slice means none.
func normalizeCategories(selected []string) ([]string, error) {
	if selected == nil {
		return nil, nil
	}
	options := CategoryOptions()
	if len(selected) > len(options) {
		return nil, errors.New("Too many notification categories")
	}
	seen := map[string]bool{}
	for _, id := range selected {
		if _, ok := actions[id]; !ok || seen[id] {
			return nil, errors.New("Choose distinct valid notification categories")
		}
		seen[id] = true
	}
	if len(seen) == len(options) {
		return nil, nil
	}
	result := []string{}
	for _, c := range options {
		if seen[c.ID] {
			result = append(result, c.ID)
		}
	}
	return result, nil
}
func effectiveCategories(selected []string) []string {
	if selected != nil {
		return append([]string{}, selected...)
	}
	result := []string{}
	for _, c := range CategoryOptions() {
		result = append(result, c.ID)
	}
	return result
}
func sameCategories(a, b []string) bool { return (a == nil) == (b == nil) && slices.Equal(a, b) }
func (c Config) accepts(e Event) bool {
	// A user-requested delivery test always checks the saved destination.
	return e.Scope == "webhook" && e.Action == "test" || c.Categories == nil || slices.Contains(c.Categories, e.Scope)
}

package bot

import (
	"os"
	"strings"
	"tasktracker/internal/model"
)

// Public examples have no real names or Telegram usernames.
// Each deployment supplies its own optional usernames through the environment.
func configuredHandle(name string) string {
	variable := ""
	switch name {
	case model.NameMemberOne:
		variable = "MEMBER_ONE_USERNAME"
	case model.NameMemberTwo:
		variable = "MEMBER_TWO_USERNAME"
	default:
		return ""
	}
	value := strings.TrimPrefix(strings.TrimSpace(os.Getenv(variable)), "@")
	if value == "" {
		return ""
	}
	return "@" + value
}

func mention(name string) string {
	if value := configuredHandle(name); value != "" {
		return value
	}
	return name
}

func mentionAll() string {
	result := make([]string, 0, len(teamNames()))
	for _, name := range teamNames() {
		result = append(result, mention(name))
	}
	return strings.Join(result, " ")
}

func nameFromUsername(username string) string {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return ""
	}
	for _, name := range teamNames() {
		expected := strings.TrimPrefix(configuredHandle(name), "@")
		if expected != "" && strings.EqualFold(username, expected) {
			return name
		}
	}
	return ""
}

func teamNames() []string { return []string{model.NameMemberOne, model.NameMemberTwo} }

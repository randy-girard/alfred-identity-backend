package appprocess

import (
	"fmt"
	"strings"
)

// Role selects which parts of the daemon this OS process runs.
type Role string

const (
	RoleAll     Role = "all"
	RoleWeb     Role = "web"
	RoleDiscord Role = "discord"
)

// ParseRole reads web, discord, or all (default all).
func ParseRole(raw string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(RoleAll):
		return RoleAll, nil
	case string(RoleWeb):
		return RoleWeb, nil
	case string(RoleDiscord):
		return RoleDiscord, nil
	default:
		return "", fmt.Errorf("unknown process %q (use web, discord, or all)", raw)
	}
}

// FromArgsAndEnv prefers -process / --process, then PROCESS, then all.
func FromArgsAndEnv(args []string, processEnv string) (Role, error) {
	raw := strings.TrimSpace(processEnv)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-process" || a == "--process":
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value (web, discord, or all)", a)
			}
			raw = args[i+1]
			i++
		case strings.HasPrefix(a, "-process="):
			raw = strings.TrimPrefix(a, "-process=")
		case strings.HasPrefix(a, "--process="):
			raw = strings.TrimPrefix(a, "--process=")
		}
	}
	return ParseRole(raw)
}

func (r Role) WantsHTTP() bool {
	return r == RoleAll || r == RoleWeb
}

func (r Role) WantsSSOHub() bool {
	return r.WantsHTTP()
}

func (r Role) WantsDiscordGateway() bool {
	return r == RoleAll || r == RoleDiscord
}

func (r Role) WantsShareREST() bool {
	return r == RoleWeb
}

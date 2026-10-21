package lifecycle

import "strings"

// ServiceName chooses one of the explicitly supported SCM service names.
// Aegis is a single binary; each Windows service has its own instance.
func ServiceName(args []string) (string, bool) {
	if len(args) < 1 || args[0] != "start" {
		return "", false
	}
	component := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--component" {
			if i+1 >= len(args) || component != "" {
				return "", false
			}
			component = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--component=") {
			if component != "" {
				return "", false
			}
			component = strings.TrimPrefix(arg, "--component=")
		}
	}
	switch component {
	case "agent":
		return "AegisAgent", true
	case "receiver":
		return "AegisReceiver", true
	default:
		return "", false
	}
}

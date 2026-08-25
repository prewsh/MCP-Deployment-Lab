// Package recovery produces proposals only; it never changes infrastructure.
package recovery

import (
	"fmt"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"regexp"
	"strconv"
)

type Proposal struct {
	Detected, Current, Proposed string
	RequiredActions             []string `json:"required_actions"`
	Risk                        string
}

var portPattern = regexp.MustCompile(`(?i)port[^0-9]{0,12}([0-9]{2,5})`)

func FromFailure(plan planning.Plan, observation string) (Proposal, bool) {
	match := portPattern.FindStringSubmatch(observation)
	if len(match) != 2 {
		return Proposal{}, false
	}
	port, err := strconv.Atoi(match[1])
	if err != nil || port < 1 || port > 65535 || port == plan.Port {
		return Proposal{}, false
	}
	return Proposal{Detected: "Port mismatch detected from deployment evidence.", Current: fmt.Sprintf("%d", plan.Port), Proposed: fmt.Sprintf("%d", port), RequiredActions: []string{"update project configuration", "redeploy", "verify health"}, Risk: "MEDIUM"}, true
}

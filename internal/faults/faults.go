// Package faults contains the narrowly scoped Milestone 5 experiments.
package faults

import (
	"errors"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
)

type Mode string

const LostResponse Mode = "LOST_RESPONSE_AFTER_DEPLOY"

func Validate(config policy.Config, plan planning.Plan, mode Mode) error {
	if mode == "" {
		return nil
	}
	if mode != LostResponse {
		return errors.New("unsupported fault mode")
	}
	if !config.FaultsEnabled {
		return errors.New("fault injection is disabled; set ENABLE_FAULT_INJECTION=true only for the approved sandbox")
	}
	if err := policy.Validate(config, plan); err != nil {
		return err
	}
	return nil
}

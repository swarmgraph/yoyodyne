package testharness

import (
	"github.com/mason-bryant/yoyodyne/internal/landing"
)

// LandingBlock is the claim as a developer's reply carries it.
func LandingBlock(payload string) string {
	return landing.Fence + "\n" + payload + "\n```\n"
}

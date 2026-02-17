package agenterr

import "errors"

var (
	ErrApprovalRequired = errors.New("approval required")
	ErrAccessDenied     = errors.New("access denied")
	ErrAdminRole        = errors.New("admin role required")
	ErrToolNotAllowed   = errors.New("tool not allowed")
	ErrToolInvalidArgs  = errors.New("tool invalid args")
	ErrToolPreflight    = errors.New("tool preflight failed")
)

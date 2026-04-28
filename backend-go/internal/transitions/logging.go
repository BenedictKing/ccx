package transitions

import "github.com/BenedictKing/ccx/internal/statelog"

func LogStateTransition(component, scope, subject, from, to, cause string, extras ...string) {
	statelog.LogStateTransition(component, scope, subject, from, to, cause, extras...)
}

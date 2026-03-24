package cmd

import "testing"

func TestRouteCommandRejectsUnexpectedArgs(t *testing.T) {
	if err := routeCmd.Args(routeCmd, []string{"extra"}); err == nil {
		t.Fatalf("expected route command to reject extra positional args")
	}
}

package main

import "testing"

func TestRouteForUsesExactRequestedCodexModel(t *testing.T) {
	s := testServer(codexTestConfig())

	cases := []struct {
		selected string
		want     string
	}{
		{codexSolID, "z-ai/glm-5.2"},
		{codexTerraID, "big-pickle"},
		{codexLunaID, "stepfun-ai/step-3.7-flash"},
	}

	for _, tc := range cases {
		t.Run(tc.selected, func(t *testing.T) {
			route, err := s.routeFor(tc.selected)
			if err != nil {
				t.Fatal(err)
			}
			if route.Model != tc.want {
				t.Fatalf("route model = %q, want %q", route.Model, tc.want)
			}
		})
	}
}

func TestRouteForRejectsUnregisteredCodexModel(t *testing.T) {
	s := testServer(codexTestConfig())
	if _, err := s.routeFor("gpt-5.6-unknown"); err == nil {
		t.Fatal("expected unregistered Codex model to be rejected")
	}
}

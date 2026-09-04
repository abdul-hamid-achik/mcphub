package config

import "testing"

func TestLocalIntelligenceExample(t *testing.T) {
	cfg, err := Load("../../docs/public/local-intelligence.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Lazy() || len(cfg.Pin) != 1 || cfg.Pin[0] != "cortex__open_task" || len(cfg.Servers) != 4 {
		t.Fatalf("unexpected exposure: %+v", cfg)
	}
	for name, server := range cfg.Servers {
		if !server.Enabled || len(server.UseWhen) == 0 {
			t.Errorf("%s cannot be discovered", name)
		}
	}
	if cfg.Servers["codemap"].Env["CODEMAP_SEMANTIC_BACKEND"] != "vecgrep" {
		t.Fatal("duplicate semantic owner")
	}
}

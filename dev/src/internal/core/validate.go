package core

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"ksu-proxy/internal/config"
)

func ValidateSingBox(cfg config.Config) error {
	if !cfg.SingBox.ValidateBeforeRun {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.SingBox.Binary, "check", "-c", cfg.SingBox.RuntimeConfig, "-D", cfg.SingBox.ConfigDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed for %s with config dir %s: %w: %s", cfg.SingBox.RuntimeConfig, cfg.SingBox.ConfigDir, err, string(out))
	}
	return nil
}

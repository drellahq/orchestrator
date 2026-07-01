package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/drellahq/orchestrator/internal/config"
	"github.com/spf13/cobra"
)

var (
	checkBackend string
	checkFix     bool
)

var checkSetupCmd = &cobra.Command{
	Use:   "check-setup",
	Short: "Verify local development prerequisites",
	Long: `Check that tools and configuration needed for local development are present.

Uses the same config loader as other orchestrator commands, so defaults and
llm_base_url match orchestrator.yaml (including -c / --config).`,
	RunE: runCheckSetup,
}

func init() {
	checkSetupCmd.Flags().StringVar(&checkBackend, "backend", "", "sandbox backend to check (podman or gjoll; default: from config)")
	checkSetupCmd.Flags().BoolVar(&checkFix, "fix", false, "copy orchestrator.yaml.example if orchestrator.yaml is missing")
	rootCmd.AddCommand(checkSetupCmd)
}

func runCheckSetup(cmd *cobra.Command, _ []string) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	cfgPath := configPath
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(repoRoot, cfgPath)
	}

	failures := 0
	warnings := 0
	pass := func(msg string) { fmt.Printf("  \033[32m✓\033[0m %s\n", msg) }
	fail := func(msg string) {
		fmt.Printf("  \033[31m✗\033[0m %s\n", msg)
		failures++
	}
	warn := func(msg string) {
		fmt.Printf("  \033[33m!\033[0m %s\n", msg)
		warnings++
	}

	var cfg *config.Config
	if _, err := os.Stat(cfgPath); err != nil {
		if checkFix {
			example := filepath.Join(repoRoot, "orchestrator.yaml.example")
			data, copyErr := os.ReadFile(example)
			if copyErr != nil {
				return fmt.Errorf("copying example config: %w", copyErr)
			}
			if copyErr := os.WriteFile(cfgPath, data, 0644); copyErr != nil {
				return fmt.Errorf("writing config: %w", copyErr)
			}
			pass("Created orchestrator.yaml from orchestrator.yaml.example")
		} else {
			warn("orchestrator.yaml missing — run: cp orchestrator.yaml.example orchestrator.yaml")
		}
	} else {
		pass(fmt.Sprintf("orchestrator.yaml present (%s)", cfgPath))
	}

	if loaded, loadErr := config.Load(cfgPath); loadErr != nil {
		if _, statErr := os.Stat(cfgPath); statErr == nil {
			fail(fmt.Sprintf("loading config: %v", loadErr))
		}
	} else {
		cfg = loaded
	}

	backend := checkBackend
	if backend == "" && cfg != nil {
		backend = cfg.SandboxBackend
	}
	if backend == "" {
		backend = "podman"
	}
	if backend != "podman" && backend != "gjoll" {
		return fmt.Errorf("invalid backend %q (expected podman or gjoll)", backend)
	}

	fmt.Printf("Checking orchestrator local setup (backend: %s)\n\n", backend)

	binary := filepath.Join(repoRoot, "orchestrator")
	if st, err := os.Stat(binary); err == nil && st.Mode()&0111 != 0 {
		pass(fmt.Sprintf("orchestrator binary built (%s)", binary))
	} else {
		warn("orchestrator binary not found — run: go build -o orchestrator ./cmd/orchestrator")
	}

	if out, err := exec.Command("go", "version").CombinedOutput(); err != nil {
		fail("go not found — install Go 1.24+ (see README.md § Prerequisites)")
	} else {
		fields := strings.Fields(string(out))
		version := "installed"
		if len(fields) >= 3 {
			version = fields[2]
		}
		pass("go: " + version)
	}

	gjollEnv := "./configs/sandbox.tf"
	usesVertexTF := true
	needsAnthropicKey := false

	if cfg != nil {
		gjollEnv = cfg.GjollEnv
		if !cfg.UsesLocalLLM() {
			if backend == "podman" {
				needsAnthropicKey = true
			}
		}
	}

	if abs, err := filepath.Abs(gjollEnv); err == nil {
		gjollEnv = abs
	} else {
		gjollEnv = filepath.Join(repoRoot, gjollEnv)
	}
	usesVertexTF = strings.Contains(gjollEnv, "sandbox.tf") && !strings.Contains(gjollEnv, "anthropic")
	if backend == "gjoll" && !usesVertexTF && (cfg == nil || !cfg.UsesLocalLLM()) {
		needsAnthropicKey = true
	}

	switch backend {
	case "podman":
		if out, err := exec.Command("podman", "version").CombinedOutput(); err != nil {
			fail("podman not found — see README.md § Option 1: Podman Backend")
		} else {
			version := strings.TrimSpace(string(out))
			if i := strings.Index(version, "\n"); i >= 0 {
				version = version[:i]
			}
			pass("podman: " + strings.TrimPrefix(version, "Client:"))
		}
	default:
		for _, tool := range []string{"gjoll", "tofu", "virsh"} {
			if _, err := exec.LookPath(tool); err != nil {
				fail(tool + " not found — see README.md § Prerequisites and HACKING.md")
				continue
			}
			switch tool {
			case "gjoll":
				pass("gjoll: " + trimFirstLine(runOutput("gjoll", "version")))
			case "tofu":
				pass("tofu: " + trimFirstLine(runOutput("tofu", "version")))
			case "virsh":
				pass("virsh: " + trimFirstLine(runOutput("virsh", "--version")))
			}
		}

		if out, err := exec.Command("virsh", "net-info", "default").CombinedOutput(); err != nil {
			warn("libvirt default network not found — run: sudo virsh net-start default")
		} else if strings.Contains(string(out), "Active:         yes") || strings.Contains(string(out), "Active: yes") {
			pass("libvirt default network is active")
		} else {
			warn("libvirt default network is inactive — run: sudo virsh net-start default")
		}

		if usesVertexTF {
			if err := exec.Command("gcloud", "auth", "application-default", "print-access-token").Run(); err != nil {
				warn("GCP ADC not configured — gjoll_env uses sandbox.tf (Vertex AI); run: gcloud auth application-default login")
				warn("Or use Local Development Option 2 with configs/sandbox-anthropic-api.tf.example")
			} else {
				pass("GCP Application Default Credentials configured")
			}
		} else {
			pass("gjoll_env uses Anthropic API path (" + gjollEnv + ")")
		}
	}

	if cfg != nil && cfg.UsesLocalLLM() {
		baseURL := cfg.LocalLLMBaseURL()
		modelsURL := strings.TrimSuffix(baseURL, "/") + "/models"
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(modelsURL)
		if err != nil {
			fail(fmt.Sprintf("local LLM not reachable at %s — start LM Studio (or your server) and enable the API", baseURL))
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				pass(fmt.Sprintf("local LLM reachable (%s)", modelsURL))
			} else {
				fail(fmt.Sprintf("local LLM returned HTTP %d at %s", resp.StatusCode, modelsURL))
			}
		}
	}

	if needsAnthropicKey && cfg != nil {
		keyPath := cfg.AnthropicKeyFile
		if strings.HasPrefix(keyPath, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				fail("resolving Anthropic key path: " + err.Error())
			} else {
				keyPath = filepath.Join(home, keyPath[2:])
			}
		}
		if f, err := os.Open(keyPath); err != nil {
			fail(fmt.Sprintf("Anthropic API key not readable at %s — see README.md § Local Development", keyPath))
		} else {
			_ = f.Close()
			pass("Anthropic API key readable (" + keyPath + ")")
		}
	}

	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		warn("gh not authenticated — required for open_pr and daemon tasks (gh auth login)")
	} else {
		pass("gh authenticated")
	}

	fmt.Println()
	if failures > 0 {
		fmt.Printf("\033[31m%d required check(s) failed\033[0m", failures)
		if warnings > 0 {
			fmt.Printf(", %d warning(s)", warnings)
		}
		fmt.Println()
		fmt.Println("See README.md § Local Development for setup instructions.")
		if backend == "gjoll" {
			fmt.Println("Integration tests also require libvirt — see HACKING.md")
		}
		os.Exit(1)
	}

	if warnings > 0 {
		fmt.Printf("\033[33mAll required checks passed (%d warning(s))\033[0m\n", warnings)
	} else {
		fmt.Println("\033[32mAll checks passed.\033[0m")
	}

	if checkFix {
		if _, err := os.Stat(binary); err != nil {
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Printf("  cd %s\n", repoRoot)
			fmt.Println("  go build -o orchestrator ./cmd/orchestrator")
			fmt.Println("  ./orchestrator task new hello-test \"Create a hello.txt file\"")
		}
	}

	return nil
}

func runOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "installed"
	}
	return string(out)
}

func trimFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

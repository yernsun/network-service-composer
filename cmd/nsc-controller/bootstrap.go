package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/install"
)

type bootstrapSession interface {
	Plan(context.Context, bootstrap.Action) (bootstrap.Plan, error)
	Execute(context.Context, bootstrap.Plan, string, string) (bootstrap.Operation, error)
	Resume(context.Context, string) (bootstrap.Operation, error)
	Verify(context.Context, io.Writer) error
}

type bootstrapInstaller interface {
	Plan(context.Context, bootstrap.Action) (bootstrap.Plan, error)
	ExecuteMaintenance(context.Context, bootstrap.Plan, string, string) (bootstrap.Operation, error)
	ResumeMaintenance(context.Context, string) (bootstrap.Operation, error)
	Verify(context.Context, io.Writer) error
	Handoff(io.Writer) error
}

type installationSession struct {
	driver   bootstrapInstaller
	executor bootstrap.Executor
	record   *bootstrap.Record
}

func (s installationSession) Plan(ctx context.Context, action bootstrap.Action) (bootstrap.Plan, error) {
	return s.driver.Plan(ctx, action)
}
func (s installationSession) Execute(ctx context.Context, p bootstrap.Plan, approval, key string) (bootstrap.Operation, error) {
	if p.Action == bootstrap.Repair || p.Action == bootstrap.Upgrade || p.Action == bootstrap.Rollback {
		return s.driver.ExecuteMaintenance(ctx, p, approval, key)
	}
	return s.executor.Execute(ctx, s.record, p, approval, key)
}
func (s installationSession) Resume(ctx context.Context, id string) (bootstrap.Operation, error) {
	for _, operation := range s.record.Operations {
		if operation.ID == id && (operation.Plan.Action == bootstrap.Repair || operation.Plan.Action == bootstrap.Upgrade || operation.Plan.Action == bootstrap.Rollback) {
			return s.driver.ResumeMaintenance(ctx, id)
		}
	}
	return s.executor.Resume(ctx, s.record, id)
}
func (s installationSession) Verify(ctx context.Context, w io.Writer) error {
	return s.driver.Verify(ctx, w)
}

type bootstrapSessionOptions struct {
	PrepareWeb        bool
	CACertificateFile string
	CAPrivateKeyFile  string
	CredentialFile    string
	Release           *bootstrap.Release
	Components        []string
}
type bootstrapSessionFactory func(context.Context, bootstrap.Config, *bootstrap.Record, *bootstrap.Journal, string, bootstrapSessionOptions) (bootstrapSession, error)

func bootstrapCommand(args []string, input io.Reader, output io.Writer) error {
	return bootstrapCommandWithSession(args, input, output, connectBootstrap)
}

func bootstrapCommandWithSession(args []string, input io.Reader, output io.Writer, connect bootstrapSessionFactory) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nsc-controller bootstrap <init|adopt|status|verify|resume|repair|upgrade|rollback> --config FILE --state-dir DIRECTORY")
	}
	command := args[0]
	input = bufio.NewReader(input)
	switch command {
	case "init", "adopt", "status", "verify", "resume", "repair", "upgrade", "rollback":
	default:
		return fmt.Errorf("unsupported bootstrap command")
	}
	flags := flag.NewFlagSet("bootstrap "+command, flag.ContinueOnError)
	configFile := flags.String("config", "", "private installation configuration")
	stateDir := flags.String("state-dir", "/var/lib/nsc-bootstrap", "private journal and offline recovery material directory")
	planFile := flags.String("plan", "", "saved plan to approve; otherwise generate a private plan")
	approval := flags.String("approve", "", "exact saved plan digest")
	key := flags.String("idempotency-key", "", "unique operation key")
	credential := flags.String("credential-file", "", "transient private current administrator credential for verification after password rotation")
	operation := flags.String("operation", "", "recorded operation to resume")
	releaseFile := flags.String("release", "", "private compatible release manifest for an upgrade plan")
	components := flags.String("components", "", "explicit comma-separated repair components")
	prepareWeb := flags.Bool("prepare-web", false, "prepare a verified legacy installation for independent Web maintenance")
	caCertificate := flags.String("ca-cert-file", "", "original offline CA certificate for first preparation")
	caKey := flags.String("ca-key-file", "", "original offline CA private key for first preparation")
	interactive := flags.Bool("interactive", false, "guide configuration and confirm the generated plan")
	timeout := flags.Duration("timeout", 15*time.Minute, "bounded installation or verification deadline")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 {
		return fmt.Errorf("unexpected arguments or invalid timeout")
	}
	options := bootstrapSessionOptions{CredentialFile: *credential, PrepareWeb: *prepareWeb, CACertificateFile: *caCertificate, CAPrivateKeyFile: *caKey}
	if *prepareWeb && command != "upgrade" || (*caCertificate != "" || *caKey != "") && !*prepareWeb {
		return fmt.Errorf("original CA references require upgrade --prepare-web; resume uses its recorded references")
	}
	if *prepareWeb && (!filepath.IsAbs(*caCertificate) || !filepath.IsAbs(*caKey)) {
		return fmt.Errorf("first preparation requires original absolute CA certificate/key references")
	}
	if *releaseFile != "" {
		if command != "upgrade" || *planFile != "" {
			return fmt.Errorf("--release is only accepted when generating an upgrade plan")
		}
		value, err := install.DecodeMaintenanceRelease(*releaseFile)
		if err != nil {
			return err
		}
		options.Release = value
	}
	if *components != "" {
		if command != "repair" || *planFile != "" {
			return fmt.Errorf("--components is only accepted for a new repair plan")
		}
		options.Components = strings.Split(*components, ",")
	}
	if command == "status" {
		r, err := bootstrap.ReadJournal(*stateDir)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(r)
	}
	j, err := bootstrap.OpenJournal(*stateDir)
	if err != nil {
		return err
	}
	defer j.Close()
	if *interactive && *configFile == "" {
		path, err := bootstrapGuide(input, output, *stateDir)
		if err != nil {
			return err
		}
		*configFile = path
	}
	if *configFile == "" {
		return fmt.Errorf("--config is required; use --interactive for guided setup")
	}
	data, err := bootstrap.ReadPrivate(*configFile)
	if err != nil {
		return err
	}
	cfg, err := bootstrap.DecodeConfig(data)
	if err != nil {
		return err
	}
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil || len(strings.TrimSpace(string(machine))) == 0 {
		return fmt.Errorf("a stable maintenance-node machine identity is required")
	}
	r, err := j.Initialize(cfg, bootstrap.Digest(strings.TrimSpace(string(machine))))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	session, err := connect(ctx, cfg, &r, j, *stateDir, options)
	if err != nil {
		return err
	}

	if command == "verify" {
		return session.Verify(ctx, output)
	}
	if command == "resume" {
		if *operation == "" {
			for n := len(r.Operations) - 1; n >= 0; n-- {
				if r.Operations[n].State != bootstrap.Succeeded {
					*operation = r.Operations[n].ID
					break
				}
			}
		}
		if *operation == "" {
			return fmt.Errorf("there is no unfinished operation to resume")
		}
		result, err := session.Resume(ctx, *operation)
		if writeErr := json.NewEncoder(output).Encode(result); err == nil {
			err = writeErr
		}
		return err
	}
	var plan bootstrap.Plan
	if *planFile != "" {
		data, err := bootstrap.ReadPrivate(*planFile)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&plan); err != nil {
			return err
		}
		if string(plan.Action) != command {
			return fmt.Errorf("saved plan action differs from the requested command")
		}
	} else {
		if *approval != "" {
			return fmt.Errorf("--approve requires a saved --plan")
		}
		plan, err = session.Plan(ctx, bootstrap.Action(command))
		if err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		if err = bootstrap.AtomicPrivateFile(*stateDir, "plan.json", encoded); err != nil {
			return err
		}
		*planFile = filepath.Join(*stateDir, "plan.json")
	}
	if err = json.NewEncoder(output).Encode(plan); err != nil {
		return err
	}
	if *interactive && *approval == "" {
		fmt.Fprint(output, "Approve this exact plan (type its digest, or leave empty to stop): ")
		value, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		*approval = strings.TrimSpace(value)
	}
	if *approval == "" {
		fmt.Fprintln(output, "Plan saved. Execution requires --plan, --approve and --idempotency-key.")
		return nil
	}
	if *key == "" && *interactive {
		*key = plan.Hash
	}
	result, err := session.Execute(ctx, plan, *approval, *key)
	if err == nil && result.State != bootstrap.Succeeded {
		err = fmt.Errorf("installation operation remains %s; inspect status and reconcile before continuing", result.State)
	}
	if writeErr := json.NewEncoder(output).Encode(result); err == nil {
		err = writeErr
	}
	if err == nil && result.State == bootstrap.Succeeded {
		if actual, ok := session.(installationSession); ok && (plan.Action == bootstrap.Init || plan.Action == bootstrap.Adopt) {
			err = actual.driver.Handoff(output)
		}
	}
	return err
}

func bootstrapGuide(input io.Reader, output io.Writer, dir string) (string, error) {
	reader := bufio.NewReader(input)
	read := func(prompt string) (string, error) {
		fmt.Fprint(output, prompt+": ")
		value, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("incomplete guided configuration")
		}
		return strings.TrimSpace(value), nil
	}
	c := bootstrap.Config{Name: "nsc", Mode: "single", ConsolePort: 8443, NodeDir: "/var/lib/nsc"}
	mode, err := read("Mode (single/multi)")
	if err != nil {
		return "", err
	}
	c.Mode = mode
	n := 1
	if mode == "multi" {
		n = 3
	}
	for index := 0; index < n; index++ {
		node := bootstrap.Node{Name: fmt.Sprintf("node-%d", index+1), SSHPort: 22, Manager: true}
		if node.Address, err = read("Node IP address"); err != nil {
			return "", err
		}
		if node.SSHUser, err = read("SSH user (root or passwordless sudo)"); err != nil {
			return "", err
		}
		if node.SSHKeyFile, err = read("SSH key file (empty uses SSH agent)"); err != nil {
			return "", err
		}
		c.Nodes = append(c.Nodes, node)
	}
	c.MaintenanceNode, c.DataNode = c.Nodes[0].Name, c.Nodes[0].Name
	if c.KnownHostsFile, err = read("Trusted known_hosts file"); err != nil {
		return "", err
	}
	if c.Images.Controller, err = read("Controller image@sha256 digest"); err != nil {
		return "", err
	}
	if c.Images.Gateway, err = read("Gateway image@sha256 digest"); err != nil {
		return "", err
	}
	if c.Images.PostgreSQL, err = read("PostgreSQL 18 Alpine image@sha256 digest"); err != nil {
		return "", err
	}
	if err = c.Validate(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	if err = bootstrap.AtomicPrivateFile(dir, "install.yaml", data); err != nil {
		return "", err
	}
	return filepath.Join(dir, "install.yaml"), nil
}

func connectBootstrap(ctx context.Context, cfg bootstrap.Config, record *bootstrap.Record, j *bootstrap.Journal, directory string, options bootstrapSessionOptions) (bootstrapSession, error) {
	hosts := map[string]install.Engine{}
	for _, n := range cfg.Nodes {
		hosts[n.Name] = bootstrap.Host{Node: n, KnownHostsFile: cfg.KnownHostsFile, DiagnosticsDir: filepath.Join(directory+".offline", "diagnostics")}
	}
	remote, err := hosts[cfg.MaintenanceNode].Run(ctx, "cat /etc/machine-id", nil)
	if err != nil {
		return nil, err
	}
	if bootstrap.Digest(strings.TrimSpace(string(remote))) != record.MaintenanceIdentity {
		return nil, fmt.Errorf("run the executor on its recorded maintenance node; a copied journal cannot authorize takeover")
	}
	driver := &install.Installer{Config: cfg, Record: record, Journal: j, Directory: directory, Hosts: hosts, CredentialFile: options.CredentialFile, Target: options.Release, Components: options.Components, PrepareWeb: options.PrepareWeb, OriginalCACertificateFile: options.CACertificateFile, OriginalCAPrivateKeyFile: options.CAPrivateKeyFile}
	executor := bootstrap.Executor{Journal: j, Driver: driver}
	return installationSession{driver: driver, executor: executor, record: record}, nil
}

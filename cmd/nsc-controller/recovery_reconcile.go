package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func recoveryReconcile(args []string) error {
	set := flag.NewFlagSet("recovery-reconcile", flag.ContinueOnError)
	path := set.String("config", "/etc/nsc/config.yaml", "isolated recovery configuration")
	operation := set.String("operation", "", "held running Application operation ID")
	engine := set.String("expected-engine", "", "original engine ID from the private backup inventory")
	node := set.String("expected-node", "", "original node ID, if applicable")
	cluster := set.String("expected-cluster", "", "original cluster ID, if applicable")
	input := set.String("review", "", "optional existing private plan to extend")
	output := set.String("output", "", "new private plan containing reconciliation evidence")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *operation == "" || *engine == "" || *output == "" {
		return errors.New("operation, original expected-engine and new output file are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, cfg, ring, err := openRecovery(ctx, *path)
	if err != nil {
		return err
	}
	defer store.Close()
	doc, err := store.GetControlDocument(ctx, storage.RecoveryBarrierID)
	if err != nil {
		return err
	}
	var plan recovery.Plan
	if err = json.Unmarshal(doc.Payload, &plan); err != nil {
		return err
	}
	if *input != "" {
		raw, err := os.ReadFile(*input)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &plan); err != nil {
			return errors.New("invalid recovery plan JSON")
		}
	}
	current, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		return err
	}
	if plan.Format != 1 || plan.BarrierRevision != doc.Revision || current != plan.Fingerprint {
		return storage.ErrPrecondition
	}
	reader := recovery.LiveReconciler{Store: store, Keys: ring, Config: cfg}
	found := false
	for i, item := range plan.Items {
		if item.ID != *operation || item.Kind != "deployment" {
			continue
		}
		proof, err := reader.Capture(ctx, item, recovery.RuntimeIdentity{EngineID: *engine, NodeID: *node, ClusterID: *cluster})
		if err != nil {
			return err
		}
		plan.Items[i].Decision, plan.Items[i].Reconciliation = "reconcile", proof
		found = true
		break
	}
	if !found {
		return errors.New("operation is absent from the held recovery inventory")
	}
	plan.ActivationReviewed = false
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(plan); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "observed", "release": "explicit activation review and fresh runtime recheck required"})
}

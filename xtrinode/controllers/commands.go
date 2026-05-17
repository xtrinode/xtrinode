package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/internal/status"
)

// Command represents a command received via annotations
type Command struct {
	Type      CommandType
	Timestamp time.Time
	Params    map[string]string
}

// CommandType represents the type of command
type CommandType string

const (
	// CommandResume requests resuming a suspended XTrinode
	CommandResume CommandType = "resume"
	// CommandSuspend requests suspending a XTrinode
	CommandSuspend CommandType = "suspend"
	// CommandAutoSuspend indicates auto-suspend triggered
	CommandAutoSuspend CommandType = "autosuspend"
)

// ProcessCommands reads annotations, validates, updates spec, clears annotations
// This is the single point where annotation-based commands are converted to spec changes
func (r *XTrinodeReconciler) ProcessCommands(ctx context.Context, xtrinode *analyticsv1.XTrinode) ([]Command, error) {
	log := ctrl.LoggerFrom(ctx)

	if xtrinode.Annotations == nil {
		return nil, nil
	}

	commands, resumeCmd, suspendCmd, autoSuspendCmd := detectAnnotationCommands(xtrinode, log)

	// Track which command types were DETECTED (before precedence filtering)
	// These annotations must ALWAYS be cleared, even if the command lost precedence.
	// Otherwise leftover annotations fire again on next reconcile.
	detectedResume := resumeCmd != nil
	detectedSuspend := suspendCmd != nil
	detectedAutoSuspend := autoSuspendCmd != nil

	commands = resolveCommandPrecedence(commands, &resumeCmd, &suspendCmd, &autoSuspendCmd, log)

	// Only proceed if there are any detected commands (winning or not)
	if !detectedResume && !detectedSuspend && !detectedAutoSuspend {
		return commands, nil
	}

	// Validate winning commands before persisting
	if resumeCmd != nil {
		if err := r.validateResumeCommand(*resumeCmd); err != nil {
			// Set condition to inform user of invalid command
			status.SetCondition(xtrinode, status.ConditionTypeReady, metav1.ConditionFalse,
				"CommandRejected", fmt.Sprintf("Invalid resume command: %v", err))

			// Persist condition with conflict-safe retry
			key := client.ObjectKeyFromObject(xtrinode)
			if updateErr := updateStatusWithRetry(ctx, r.Client, r.Status(), key, log,
				func() client.Object { return &analyticsv1.XTrinode{} },
				func(obj client.Object) error {
					t, ok := obj.(*analyticsv1.XTrinode)
					if !ok {
						return fmt.Errorf("unexpected object type %T", obj)
					}
					status.SetCondition(t, status.ConditionTypeReady, metav1.ConditionFalse,
						"CommandRejected", fmt.Sprintf("Invalid resume command: %v", err))
					return nil
				}); updateErr != nil {
				log.Error(updateErr, "failed to update status with CommandRejected condition")
			}

			// Emit warning event
			r.EventRecorder.Warningf(xtrinode, events.ReasonResumeRequested, "Invalid resume command: %v", err)
			log.Info("Invalid resume command - leaving annotations for user to fix", "error", err)

			// Treat as non-retryable user input: return nil so no rapid requeue
			return []Command{}, nil
		}
	}

	// Build mutation: ALWAYS clear ALL detected annotations; apply only the winning command.
	mutate := r.buildSpecMutation(detectedResume, detectedSuspend, detectedAutoSuspend, resumeCmd, suspendCmd, autoSuspendCmd)

	if err := r.persistCommands(ctx, client.ObjectKeyFromObject(xtrinode), mutate, log); err != nil {
		return nil, fmt.Errorf("failed to persist commands: %w", err)
	}

	// Emit events AFTER successful persistence
	if resumeCmd != nil {
		r.EventRecorder.Normal(xtrinode, events.ReasonResumeRequested, "Resume command processed successfully")
		log.Info("Resume command persisted successfully", "timestamp", resumeCmd.Timestamp)
	}
	if suspendCmd != nil {
		r.EventRecorder.Normal(xtrinode, events.ReasonSuspendRequested, "Suspend command processed successfully")
		log.Info("Suspend command persisted successfully", "timestamp", suspendCmd.Timestamp)
	}
	if autoSuspendCmd != nil {
		log.Info("Auto-suspend command persisted successfully", "timestamp", autoSuspendCmd.Timestamp)
	}

	return commands, nil
}

// detectAnnotationCommands scans xtrinode annotations and builds the initial command set.
func detectAnnotationCommands(xtrinode *analyticsv1.XTrinode, log logr.Logger) (commands []Command, resumeCmd, suspendCmd, autoSuspendCmd *Command) {
	commands = []Command{}
	if val, ok := xtrinode.Annotations[config.ResumeRequestedAnnotation]; ok && val == "true" {
		cmd := Command{
			Type:      CommandResume,
			Timestamp: parseTimestamp(xtrinode.Annotations[config.ResumeRequestedAtAnnotation]),
			Params: map[string]string{
				"wakeMinWorkers": xtrinode.Annotations[config.WakeMinWorkersAnnotation],
				"wakeTTL":        xtrinode.Annotations[config.WakeTTLAnnotation],
			},
		}
		resumeCmd = &cmd
		commands = append(commands, cmd)
		log.Info("Detected resume command", "timestamp", cmd.Timestamp)
	}
	if val, ok := xtrinode.Annotations[config.SuspendRequestedAnnotation]; ok && val == "true" {
		cmd := Command{
			Type:      CommandSuspend,
			Timestamp: parseTimestamp(xtrinode.Annotations[config.SuspendRequestedAtAnnotation]),
		}
		suspendCmd = &cmd
		commands = append(commands, cmd)
		log.Info("Detected suspend command", "timestamp", cmd.Timestamp)
	}
	if val, ok := xtrinode.Annotations[config.AutoSuspendRequestedAnnotation]; ok && val == "true" {
		cmd := Command{
			Type:      CommandAutoSuspend,
			Timestamp: parseTimestamp(xtrinode.Annotations[config.AutoSuspendRequestedAtAnnotation]),
		}
		autoSuspendCmd = &cmd
		commands = append(commands, cmd)
		log.Info("Detected auto-suspend command", "timestamp", cmd.Timestamp)
	}
	return commands, resumeCmd, suspendCmd, autoSuspendCmd
}

// resolveCommandPrecedence applies precedence rules: manual commands beat auto-suspend; newest wins.
func resolveCommandPrecedence(commands []Command, resumeCmd, suspendCmd, autoSuspendCmd **Command, log logr.Logger) []Command {
	if len(commands) <= 1 {
		return commands
	}
	// Manual command takes precedence over auto-suspend
	if *autoSuspendCmd != nil && (*resumeCmd != nil || *suspendCmd != nil) {
		log.Info("Manual command takes precedence over auto-suspend, ignoring auto-suspend")
		*autoSuspendCmd = nil
		filtered := []Command{}
		for _, c := range commands {
			if c.Type != CommandAutoSuspend {
				filtered = append(filtered, c)
			}
		}
		commands = filtered
	}
	// Newest of resume/suspend wins
	if *resumeCmd != nil && *suspendCmd != nil {
		if (*resumeCmd).Timestamp.After((*suspendCmd).Timestamp) {
			log.Info("Resume is newer than suspend, ignoring suspend", "resumeAt", (*resumeCmd).Timestamp, "suspendAt", (*suspendCmd).Timestamp)
			*suspendCmd = nil
		} else {
			log.Info("Suspend is newer than resume, ignoring resume", "suspendAt", (*suspendCmd).Timestamp, "resumeAt", (*resumeCmd).Timestamp)
			*resumeCmd = nil
		}
		filtered := []Command{}
		for _, c := range commands {
			if (*resumeCmd != nil && c.Type == CommandResume) || (*suspendCmd != nil && c.Type == CommandSuspend) {
				filtered = append(filtered, c)
			}
		}
		commands = filtered
	}
	return commands
}

// buildSpecMutation returns the mutation function that clears detected annotations and applies the winning command.
func (r *XTrinodeReconciler) buildSpecMutation(
	detectedResume, detectedSuspend, detectedAutoSuspend bool,
	resumeCmd, suspendCmd, autoSuspendCmd *Command,
) func(*analyticsv1.XTrinode) error {
	return func(t *analyticsv1.XTrinode) error {
		if detectedResume {
			delete(t.Annotations, config.ResumeRequestedAnnotation)
			delete(t.Annotations, config.ResumeRequestedAtAnnotation)
		}
		if detectedSuspend {
			delete(t.Annotations, config.SuspendRequestedAnnotation)
			delete(t.Annotations, config.SuspendRequestedAtAnnotation)
		}
		if detectedAutoSuspend {
			delete(t.Annotations, config.AutoSuspendRequestedAnnotation)
			delete(t.Annotations, config.AutoSuspendRequestedAtAnnotation)
		}
		if resumeCmd == nil && (detectedResume || detectedSuspend || detectedAutoSuspend) {
			delete(t.Annotations, config.WakeMinWorkersAnnotation)
			delete(t.Annotations, config.WakeTTLAnnotation)
		}
		if resumeCmd != nil {
			if err := r.applyResumeCommand(t, *resumeCmd); err != nil {
				return fmt.Errorf("failed to apply resume command: %w", err)
			}
			// Leave wake annotations (WakeMinWorkers, WakeTTL) for reconcileResume to consume
		}
		if suspendCmd != nil {
			t.Spec.Suspended = true
		}
		if autoSuspendCmd != nil {
			t.Spec.Suspended = true
		}
		return nil
	}
}

// validateResumeCommand validates resume command parameters before applying
func (r *XTrinodeReconciler) validateResumeCommand(cmd Command) error {
	// Validate wakeMinWorkers
	if wakeMinWorkers := cmd.Params["wakeMinWorkers"]; wakeMinWorkers != "" {
		workers, err := strconv.ParseInt(wakeMinWorkers, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid wakeMinWorkers: %w", err)
		}
		if workers < 0 {
			return fmt.Errorf("wakeMinWorkers must be >= 0, got %d", workers)
		}
	}

	// Validate wakeTTL
	if wakeTTL := cmd.Params["wakeTTL"]; wakeTTL != "" {
		duration, err := time.ParseDuration(wakeTTL)
		if err != nil {
			return fmt.Errorf("invalid wakeTTL: %w", err)
		}
		if duration < 0 {
			return fmt.Errorf("wakeTTL must be >= 0, got %v", duration)
		}
	}

	return nil
}

// applyResumeCommand applies resume command to spec
// Sets suspended=false only. Wake parameters stay in annotations for reconcileResume to consume.
// Following principle: annotations → status (via reconcileResume), then annotations cleared
func (r *XTrinodeReconciler) applyResumeCommand(xtrinode *analyticsv1.XTrinode, cmd Command) error {
	// Only set suspended=false in spec
	// DO NOT write wake params to spec - they are ephemeral command overrides
	xtrinode.Spec.Suspended = false

	// Wake parameters remain in annotations temporarily
	// reconcileResume will:
	// 1. Read wake params from annotations (one-time override) or spec (defaults)
	// 2. Set status.wake (ephemeral state)
	// 3. Clear wake annotations
	// This prevents command overrides from becoming permanent spec config

	return nil
}

// persistCommands persists command-induced spec changes with conflict retry.
// Each attempt fetches a fresh copy, re-applies the mutation, then updates.
// Using a no-op refreshFn because the update func already does its own Get,
// avoiding a wasted extra API call per retry.
func (r *XTrinodeReconciler) persistCommands(ctx context.Context, key client.ObjectKey, mutate func(*analyticsv1.XTrinode) error, log logr.Logger) error {
	return retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
		func() error { return nil }, // refresh is folded into the update func below
		func() error {
			fresh := &analyticsv1.XTrinode{}
			if err := r.Get(ctx, key, fresh); err != nil {
				return err
			}
			if err := mutate(fresh); err != nil {
				return err
			}
			return r.Update(ctx, fresh)
		},
	)
}

// parseTimestamp parses RFC3339 timestamp from annotation
func parseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

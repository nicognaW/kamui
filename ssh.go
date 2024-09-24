package main

// code provided by ChatGPT o1-mini

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

// ConnectSSHOptions holds configuration parameters for the ConnectSSH function.
type ConnectSSHOptions struct {
	User         string
	TargetIP     string
	MaxRetries   int
	InitialDelay time.Duration
	OnGiveUp     func(error) // Callback triggered when giving up after retries
}

// connectSSH attempts to establish an SSH connection to the targetIP as the specified user.
// It retries with exponential backoff until successful, the context is canceled, or maxRetries is reached.
// If it gives up after maxRetries, it triggers the onGiveUp callback.
func ConnectSSH(ctx context.Context, opts ConnectSSHOptions) error {
	var attempt int
	delay := opts.InitialDelay
	var lastErr error

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Proceed with SSH attempt
		}

		// Create a command with a timeout to prevent hanging
		cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		sshTarget := fmt.Sprintf("%s@%s", opts.User, opts.TargetIP)
		cmd := exec.CommandContext(cmdCtx, "ssh", "-o", "StrictHostKeyChecking=no", sshTarget)

		// Connect the SSH command's input and output to the current process
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Inherit the environment variables
		cmd.Env = os.Environ()

		log.Printf("Attempt %d: Connecting to %s...\n", attempt+1, sshTarget)
		err := cmd.Run()
		if err == nil {
			log.Println("SSH connection established successfully.")
			return nil
		}

		attempt++
		lastErr = err
		log.Printf("Attempt %d: Failed to connect SSH to %s: %v", attempt, sshTarget, err)

		if opts.MaxRetries > 0 && attempt >= opts.MaxRetries {
			// Trigger the onGiveUp callback if provided
			if opts.OnGiveUp != nil {
				opts.OnGiveUp(lastErr)
			}
			return fmt.Errorf("failed to connect SSH after %d attempts: last error: %w", attempt, lastErr)
		}

		log.Printf("Retrying in %v...\n", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Exponential backoff
			delay *= 2
			if delay > 2*time.Minute {
				delay = 2 * time.Minute
			}
		}
	}
}

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go/aws"
)

var accessKeyID = ""
var secretAccessKey = ""
var region = ""
var prefixStr = ""
var postfixStr = ""

var code = os.Getenv("CODE")

var client = ec2.New(ec2.Options{
	Region:      region,
	Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
})

func main() {
	if code != "" {
		loadCode()
		client = ec2.New(ec2.Options{
			Region:      region,
			Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		})
	}
	var ec2Filters = []types.Filter{
		{
			Name:   aws.String("tag:Name"),
			Values: []string{prefixStr + "*" + postfixStr},
		},
	}

	instance, err := getTargetInstance(ec2Filters)
	if err != nil {
		log.Fatalf("failed to get target instance, %v", err)
	}

	if instance.State.Name != types.InstanceStateNameRunning {
		err := chStateTargetInstance(ec2Filters, types.InstanceStateNameRunning)
		if err != nil {
			log.Printf("failed to change instance state, %v", err)
		}
	}

	defer func() {
		err = chStateTargetInstance(ec2Filters, types.InstanceStateNameStopped)
		if err != nil {
			log.Fatalf("failed to change instance state, %v", err)
		}
	}()

	targetIP, err := getTargetIP(ec2Filters)
	if err != nil {
		log.Printf("failed to get target IP, %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err = ConnectSSH(ctx, ConnectSSHOptions{
		User:         "ubuntu",
		TargetIP:     targetIP,
		MaxRetries:   5,
		InitialDelay: 10 * time.Second,
	})
	if err != nil {
		log.Printf("failed to connect SSH, %v\n", err)
	}
}

func loadCode() {
	decoded, err := base64.StdEncoding.DecodeString(code)
	if err != nil {
		log.Fatalf("invalid code, %v", fmt.Errorf("failed to decode base64, %v", err))
	}
	var unmarshaled map[string]any
	err = json.Unmarshal(decoded, &unmarshaled)
	if err != nil {
		log.Fatalf("invalid code, %v", fmt.Errorf("failed to unmarshal json, %v", err))
	}
	if val, ok := unmarshaled["accessKeyID"].(string); ok {
		accessKeyID = val
	}
	if val, ok := unmarshaled["secretAccessKey"].(string); ok {
		secretAccessKey = val
	}
	if val, ok := unmarshaled["region"].(string); ok {
		region = val
	}
	if val, ok := unmarshaled["prefixStr"].(string); ok {
		prefixStr = val
	}
	if val, ok := unmarshaled["postfixStr"].(string); ok {
		postfixStr = val
	}
}

func getTargetIP(ec2Filters []types.Filter) (string, error) {
	log.Printf("Getting target IP address...\n")
	instance, err := getTargetInstance(ec2Filters)
	if err != nil {
		return "", fmt.Errorf("failed to get target instance, %v", err)
	}
	if instance.State.Name != types.InstanceStateNameRunning {
		return "", fmt.Errorf("target instance is not running, but %s", instance.State.Name)
	}
	if instance.PublicIpAddress == nil {
		return "", fmt.Errorf("target instance does not have public IP address")
	}
	targetIP := *instance.PublicDnsName
	return targetIP, nil
}

var _instance_cache *types.Instance

func clearInstanceCache() {
	_instance_cache = nil
}

func getTargetInstance(ec2Filters []types.Filter) (*types.Instance, error) {
	if _instance_cache != nil {
		return _instance_cache, nil
	}
	resp, err := client.DescribeInstances(context.TODO(), &ec2.DescribeInstancesInput{
		Filters: ec2Filters,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe instances, %v", err)
	}
	if len(resp.Reservations) != 1 {
		return nil, fmt.Errorf("expected 1 reservation, got %d: %+v", len(resp.Reservations), resp.Reservations)
	}

	reservation := resp.Reservations[0]
	if len(reservation.Instances) != 1 {
		return nil, fmt.Errorf("expected 1 instance, got %d, %+v", len(reservation.Instances), reservation.Instances)
	}

	instance := reservation.Instances[0]
	instanceName := getTagValue(instance.Tags, "Name")
	if !strings.HasPrefix(instanceName, prefixStr) || !strings.HasSuffix(instanceName, postfixStr) {
		return nil, fmt.Errorf("expected instance name to start with %s and end with %s, got %s", prefixStr, postfixStr, instanceName)
	}
	_instance_cache = &instance
	return &instance, nil
}

func chStateTargetInstance(ec2Filters []types.Filter, state types.InstanceStateName) error {
	log.Printf("Changing target Instance state to %s...\n", state)
	instance, err := getTargetInstance(ec2Filters)
	if err != nil {
		return fmt.Errorf("failed to get target instance, %v", err)
	}
	if instance.State.Name == types.InstanceStateNameShuttingDown || instance.State.Name == types.InstanceStateNameTerminated {
		return fmt.Errorf("unsupported state %s", instance.State.Name)
	}

	switch state {
	case types.InstanceStateNameStopping:
		state = types.InstanceStateNameStopped
		if instance.State.Name == types.InstanceStateNameStopping ||
			instance.State.Name == types.InstanceStateNameStopped {
			log.Printf("Instance is already at %s state\n", state)
			break
		}
		fallthrough
	case types.InstanceStateNameStopped:
		result, err := client.StopInstances(context.TODO(), &ec2.StopInstancesInput{
			InstanceIds: []string{*instance.InstanceId},
		})

		if err != nil {
			return fmt.Errorf("failed to stop instance, %v", err)
		}
		if len(result.StoppingInstances) != 1 {
			return fmt.Errorf("expected 1 stopping instance, got %d: %+v", len(result.StoppingInstances), result.StoppingInstances)
		}

	case types.InstanceStateNamePending:
		state = types.InstanceStateNameRunning
		if instance.State.Name == types.InstanceStateNamePending ||
			instance.State.Name == types.InstanceStateNameRunning {
			log.Printf("Instance is already at %s state\n", state)
			break
		}
		fallthrough
	case types.InstanceStateNameRunning:
		result, err := client.StartInstances(context.TODO(), &ec2.StartInstancesInput{
			InstanceIds: []string{*instance.InstanceId},
		})

		if err != nil {
			return fmt.Errorf("failed to stop instance, %v", err)
		}
		if len(result.StartingInstances) != 1 {
			return fmt.Errorf("expected 1 stopping instance, got %d: %+v", len(result.StartingInstances), result.StartingInstances)
		}

	case types.InstanceStateNameShuttingDown, types.InstanceStateNameTerminated:
		return fmt.Errorf("unsupported state %s", state)
	}

	timeout := 2 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	log.Printf("Waiting for the instance to be %s...\n", state)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("monitoring stopped due to timeout: %v", ctx.Err())

		case <-time.After(600 * time.Millisecond):
			clearInstanceCache()
			instance, err := getTargetInstance(ec2Filters)
			if err != nil {
				return fmt.Errorf("failed to get target instance when monitoring its stopping status, %v", err)
			}

			if instance.State.Name == state {
				fmt.Print("\n")
				log.Printf("Instance %s is now %s\n", *instance.InstanceId, state)
				return nil
			}
			fmt.Print(".")
		}
	}
}

func getTagValue(tags []types.Tag, key string) string {
	for _, tag := range tags {
		if *tag.Key == key {
			return *tag.Value
		}
	}
	return ""
}

package cnc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AWS management uses the controller's EC2 role; no cloud credentials enter
// dashboard requests or the CNC database.
type awsIntegration struct {
	mu           sync.Mutex
	configPath   string
	dataDir      string
	call         func(context.Context, ...string) ([]byte, error)
	prepareTools func(context.Context) error
}

type awsClusterConfig struct {
	Region               string  `json:"region"`
	Cluster              string  `json:"cluster"`
	InstanceType         string  `json:"instance_type"`
	WorkerInstanceType   string  `json:"worker_instance_type"`
	ControllerPrivateIP  string  `json:"controller_private_ip"`
	SubnetID             string  `json:"subnet_id"`
	WorkerSecurityGroup  string  `json:"worker_security_group"`
	KeyName              string  `json:"key_name"`
	AMI                  string  `json:"ami"`
	RootDevice           string  `json:"root_device"`
	HourlyPriceUSD       float64 `json:"hourly_price_usd"`
	WorkerHourlyPriceUSD float64 `json:"worker_hourly_price_usd"`
}

type awsInstance struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Type      string `json:"type"`
	PrivateIP string `json:"private_ip"`
	PublicIP  string `json:"public_ip"`
}

func newAWSIntegration(path string) *awsIntegration {
	return &awsIntegration{configPath: path, call: awsCLI}
}

func awsCLI(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "aws", args...)
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			message := strings.TrimSpace(string(exit.Stderr))
			if len(message) > 800 {
				message = message[:800]
			}
			return nil, fmt.Errorf("AWS rejected request: %s", message)
		}
		return nil, fmt.Errorf("AWS CLI unavailable or timed out: %w", err)
	}
	return output, nil
}

func (a *awsIntegration) config() (awsClusterConfig, error) {
	var config awsClusterConfig
	contents, err := os.ReadFile(a.configPath)
	if err != nil {
		return config, fmt.Errorf("AWS controller integration is not installed")
	}
	if err := json.Unmarshal(contents, &config); err != nil {
		return config, fmt.Errorf("AWS controller configuration is invalid")
	}
	if config.Region != "us-east-2" || config.Cluster != "cnc-aws" || config.InstanceType != "m7i-flex.large" || config.WorkerInstanceType != "c7i-flex.large" ||
		net.ParseIP(config.ControllerPrivateIP) == nil || config.SubnetID == "" ||
		config.WorkerSecurityGroup == "" || config.KeyName == "" || config.AMI == "" || config.RootDevice == "" {
		return config, fmt.Errorf("AWS controller configuration is incomplete")
	}
	return config, nil
}

func (a *awsIntegration) run(ctx context.Context, region string, args ...string) ([]byte, error) {
	command := append(append([]string{}, args...), "--region", region, "--output", "json")
	return a.call(ctx, command...)
}

func (a *awsIntegration) instances(ctx context.Context, config awsClusterConfig) ([]awsInstance, error) {
	output, err := a.run(ctx, config.Region, "ec2", "describe-instances", "--filters",
		"Name=tag:CNCCluster,Values="+config.Cluster,
		"Name=instance-state-name,Values=pending,running,stopping,stopped")
	if err != nil {
		return nil, err
	}
	var response struct {
		Reservations []struct {
			Instances []struct {
				ID        string `json:"InstanceId"`
				Type      string `json:"InstanceType"`
				PrivateIP string `json:"PrivateIpAddress"`
				PublicIP  string `json:"PublicIpAddress"`
				State     struct {
					Name string `json:"Name"`
				} `json:"State"`
				Tags []struct {
					Key   string `json:"Key"`
					Value string `json:"Value"`
				} `json:"Tags"`
			} `json:"Instances"`
		} `json:"Reservations"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("AWS instance response is invalid")
	}
	instances := []awsInstance{}
	for _, reservation := range response.Reservations {
		for _, item := range reservation.Instances {
			name := ""
			for _, tag := range item.Tags {
				if tag.Key == "Name" {
					name = tag.Value
				}
			}
			instances = append(instances, awsInstance{ID: item.ID, Name: name,
				State: item.State.Name, Type: item.Type, PrivateIP: item.PrivateIP, PublicIP: item.PublicIP})
		}
	}
	return instances, nil
}

func awsJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *awsIntegration) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, err error) { awsJSON(w, status, map[string]string{"error": err.Error()}) }
	config, err := a.config()
	action := strings.TrimPrefix(r.URL.Path, "/api/aws/")
	if action == "settings" && r.Method == http.MethodGet {
		if err != nil {
			awsJSON(w, 200, map[string]any{"configured": false, "reason": err.Error()})
			return
		}
		identity, check := a.run(r.Context(), config.Region, "sts", "get-caller-identity")
		if check != nil {
			awsJSON(w, 200, map[string]any{"configured": false, "reason": check.Error()})
			return
		}
		var caller struct {
			Account string `json:"Account"`
		}
		if json.Unmarshal(identity, &caller) != nil {
			fail(502, fmt.Errorf("AWS identity response is invalid"))
			return
		}
		awsJSON(w, 200, map[string]any{"configured": true, "account": caller.Account,
			"region": config.Region, "instance_type": config.InstanceType,
			"worker_instance_type":    config.WorkerInstanceType,
			"hourly_price_usd":        config.HourlyPriceUSD,
			"worker_hourly_price_usd": config.WorkerHourlyPriceUSD})
		return
	}
	if err != nil {
		fail(503, err)
		return
	}
	if action == "instances" && r.Method == http.MethodGet {
		instances, err := a.instances(r.Context(), config)
		if err != nil {
			fail(502, err)
			return
		}
		awsJSON(w, 200, instances)
		return
	}
	if action == "quota" && r.Method == http.MethodGet {
		output, err := a.run(r.Context(), config.Region, "service-quotas", "get-service-quota",
			"--service-code", "ec2", "--quota-code", "L-1216C47A")
		if err != nil {
			fail(502, err)
			return
		}
		var response struct {
			Quota struct {
				Value float64 `json:"Value"`
			} `json:"Quota"`
		}
		if json.Unmarshal(output, &response) != nil {
			fail(502, fmt.Errorf("AWS quota response is invalid"))
			return
		}
		awsJSON(w, 200, map[string]float64{"standard_vcpus": response.Quota.Value})
		return
	}
	if action == "workers" && r.Method == http.MethodPost {
		a.mu.Lock()
		defer a.mu.Unlock()
		var input struct {
			Label       string `json:"label"`
			Count       int    `json:"count"`
			ConfirmCost bool   `json:"confirm_cost"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
			fail(400, fmt.Errorf("invalid worker request"))
			return
		}
		if !input.ConfirmCost {
			fail(400, fmt.Errorf("confirm the displayed EC2 hourly cost before creating workers"))
			return
		}
		labels, err := linodeWorkerLabels(input.Label, input.Count)
		if err != nil {
			fail(400, err)
			return
		}
		current, err := a.instances(r.Context(), config)
		if err != nil {
			fail(502, err)
			return
		}
		for _, item := range current {
			if slicesContainLabel(labels, item.Name) {
				fail(409, fmt.Errorf("worker name already exists; refresh before retrying"))
				return
			}
		}
		if a.prepareTools != nil {
			if err := a.prepareTools(r.Context()); err != nil {
				fail(503, err)
				return
			}
		}
		created := []awsInstance{}
		for index, name := range labels {
			script, err := linodeWorkerScript(name, config.ControllerPrivateIP+":9090",
				config.ControllerPrivateIP+":8080")
			if err != nil {
				fail(400, err)
				return
			}
			policyPassword, policyErr := workerRootPassword(a.dataDir)
			if policyErr != nil {
				fail(503, policyErr)
				return
			}
			if policyPassword != "" {
				setup, setupErr := workerPasswordSetup(policyPassword)
				if setupErr != nil {
					fail(503, setupErr)
					return
				}
				if saveErr := saveWorkerPassword(a.dataDir, name, policyPassword); saveErr != nil {
					fail(503, saveErr)
					return
				}
				script += setup
			}
			tokenBytes := make([]byte, 16)
			if _, err := rand.Read(tokenBytes); err != nil {
				fail(500, err)
				return
			}
			mappings, _ := json.Marshal([]map[string]any{{"DeviceName": config.RootDevice,
				"Ebs": map[string]any{"VolumeSize": 40, "VolumeType": "gp3",
					"DeleteOnTermination": true, "Encrypted": true}}})
			tags := fmt.Sprintf("ResourceType=instance,Tags=[{Key=Name,Value=%s},{Key=CNCCluster,Value=%s}]", name, config.Cluster)
			output, err := a.run(r.Context(), config.Region, "ec2", "run-instances",
				"--image-id", config.AMI, "--instance-type", config.WorkerInstanceType,
				"--count", "1", "--key-name", config.KeyName, "--subnet-id", config.SubnetID,
				"--iam-instance-profile", "Name=cnc-aws-worker-ssm",
				"--security-group-ids", config.WorkerSecurityGroup, "--associate-public-ip-address",
				"--block-device-mappings", string(mappings), "--metadata-options",
				"HttpEndpoint=enabled,HttpTokens=required", "--tag-specifications", tags,
				"--client-token", hex.EncodeToString(tokenBytes), "--user-data", script)
			if err != nil {
				awsJSON(w, http.StatusMultiStatus, map[string]any{"requested": len(labels),
					"created": created, "error": err.Error(), "uncertain_label": name,
					"not_attempted": labels[index+1:]})
				return
			}
			var response struct {
				Instances []struct {
					ID string `json:"InstanceId"`
				} `json:"Instances"`
			}
			if json.Unmarshal(output, &response) != nil || len(response.Instances) != 1 {
				awsJSON(w, http.StatusMultiStatus, map[string]any{"requested": len(labels),
					"created": created, "error": "AWS creation response could not be parsed",
					"uncertain_label": name, "not_attempted": labels[index+1:]})
				return
			}
			created = append(created, awsInstance{ID: response.Instances[0].ID, Name: name,
				State: "pending", Type: config.WorkerInstanceType})
		}
		awsJSON(w, http.StatusCreated, map[string]any{"requested": len(labels),
			"created": created, "not_attempted": []string{}})
		return
	}
	fail(405, fmt.Errorf("method or endpoint not supported"))
}

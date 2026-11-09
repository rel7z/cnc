package cnc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testAWSIntegration(t *testing.T, call func(context.Context, ...string) ([]byte, error)) *awsIntegration {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aws-config.json")
	config := awsClusterConfig{Region: "us-east-2", Cluster: "cnc-aws", InstanceType: "m7i-flex.large", WorkerInstanceType: "c7i-flex.large",
		ControllerPrivateIP: "172.31.1.4", SubnetID: "subnet-123", WorkerSecurityGroup: "sg-123",
		KeyName: "cnc-key", AMI: "ami-123", RootDevice: "/dev/sda1", HourlyPriceUSD: 0.09576, WorkerHourlyPriceUSD: 0.08479}
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	a := newAWSIntegration(path)
	a.call = call
	return a
}

func TestAWSIntegrationListsClusterInstances(t *testing.T) {
	a := testAWSIntegration(t, func(_ context.Context, args ...string) ([]byte, error) {
		if !slices.Contains(args, "Name=tag:CNCCluster,Values=cnc-aws") {
			t.Fatalf("missing cluster filter: %v", args)
		}
		return []byte(`{"Reservations":[{"Instances":[{"InstanceId":"i-123","InstanceType":"m7i-flex.large","PrivateIpAddress":"172.31.1.7","State":{"Name":"running"},"Tags":[{"Key":"Name","Value":"cnc-aws-worker-1"}]}]}]}`), nil
	})
	r := httptest.NewRequest(http.MethodGet, "/api/aws/instances", nil)
	w := httptest.NewRecorder()
	a.handle(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"cnc-aws-worker-1"`) {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
	}
}

func TestAWSIntegrationRequiresExplicitCostConfirmation(t *testing.T) {
	a := testAWSIntegration(t, func(_ context.Context, args ...string) ([]byte, error) {
		t.Fatalf("AWS must not be called without cost confirmation: %v", args)
		return nil, nil
	})
	r := httptest.NewRequest(http.MethodPost, "/api/aws/workers", strings.NewReader(`{"label":"cnc-aws-worker-4","count":1}`))
	w := httptest.NewRecorder()
	a.handle(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "confirm") {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
	}
}

func TestAWSIntegrationWorkerUsesPinnedInfrastructure(t *testing.T) {
	var launch []string
	a := testAWSIntegration(t, func(_ context.Context, args ...string) ([]byte, error) {
		if slices.Contains(args, "describe-instances") {
			return []byte(`{"Reservations":[]}`), nil
		}
		if slices.Contains(args, "run-instances") {
			launch = append([]string{}, args...)
			return []byte(`{"Instances":[{"InstanceId":"i-new"}]}`), nil
		}
		t.Fatalf("unexpected AWS call: %v", args)
		return nil, nil
	})
	r := httptest.NewRequest(http.MethodPost, "/api/aws/workers", strings.NewReader(`{"label":"cnc-aws-worker-4","count":1,"confirm_cost":true}`))
	w := httptest.NewRecorder()
	a.handle(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "i-new") {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
	}
	for _, pinned := range []string{"ami-123", "c7i-flex.large", "subnet-123", "sg-123", "cnc-key", "us-east-2"} {
		if !slices.Contains(launch, pinned) {
			t.Errorf("launch missing %s: %v", pinned, launch)
		}
	}
	userData := launch[slices.Index(launch, "--user-data")+1]
	if !strings.Contains(userData, "cnc-aws-worker-4") || !strings.Contains(userData, "172.31.1.4") {
		t.Fatalf("worker bootstrap is incomplete")
	}
}

package modelclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/predict" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"request_id":"abc","device":"cpu","parallel":true,"combined_action":"review","combine_policy":"consensus","models":[{"model":"lite","id":"1","sexual_harm_probability":0.2,"action":"review","semantic_gate":0.5,"rule_hits":[],"pass_threshold":0.15,"block_threshold":0.5,"latency_ms":1},{"model":"macbert","id":"1","sexual_harm_probability":0.8,"action":"block","semantic_gate":0.6,"rule_hits":[],"pass_threshold":0.15,"block_threshold":0.5,"latency_ms":2}]}`))
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	prediction, err := client.Check(context.Background(), "private text")
	if err != nil {
		t.Fatal(err)
	}
	if len(prediction.Models) != 2 || prediction.CombinedAction != "review" || prediction.CombinePolicy != "consensus" {
		t.Fatalf("unexpected prediction: %+v", prediction)
	}
}

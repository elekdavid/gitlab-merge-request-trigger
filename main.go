package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"strconv"
)

type project struct {
	Name    string `json:"name"`
	WebURL  string `json:"web_url"`
	HTTPURL string `json:"http_url"`
}

type commit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type objectAttributes struct {
	ID              int64   `json:"id"`
	IID             int64   `json:"iid"`
	TargetBranch    string  `json:"target_branch"`
	SourceBranch    string  `json:"source_branch"`
	SourceProjectID int64   `json:"source_project_id"`
	State           string  `json:"state"`
	Source          project `json:"source"`
	Target          project `json:"target"`
	LastCommit      commit  `json:"last_commit"`
	Action          string  `json:"action"`
}

type webhookRequest struct {
	ObjectKind string           `json:"object_kind"`
	Attributes objectAttributes `json:"object_attributes"`
}

var listenAddr = flag.String("listen", ":8080", "HTTP listen address")
var triggerToken = flag.String("token", "c339b1b59e2d4a410a955080840b33", "HTTP trigger token")

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var webhook webhookRequest
	err := json.NewDecoder(r.Body).Decode(&webhook)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if webhook.ObjectKind != "merge_request" {
		http.Error(w, "We support merge_request only", http.StatusBadRequest)
		return
	}

	if webhook.Attributes.Action != "open" && webhook.Attributes.Action != "reopen" && webhook.Attributes.Action != "update" {
		http.Error(w, "We support only open, reopen and update action", http.StatusBadRequest)
		return
	}

	log.Println(webhook.Attributes.ID, webhook.Attributes.State, webhook.Attributes.Source.Name, webhook.Attributes.LastCommit.ID, webhook.Attributes.LastCommit.Message)

	values := make(url.Values)
	values.Set("token", *triggerToken)
	values.Set("ref", webhook.Attributes.SourceBranch)
	values.Set("variables[CI_MERGE_REQUEST]", "true")
	values.Set("variables[CI_MERGE_REQUEST_ID]", strconv.Itoa(webhook.Attributes.ID))
	values.Set("variables[CI_MERGE_REQUEST_IID]", strconv.Itoa(webhook.Attributes.IID))
	values.Set("variables[CI_MERGE_REQUEST_ACTION]", webhook.Attributes.Action)
	values.Set("variables[CI_MERGE_REQUEST_STATE]", webhook.Attributes.State)
	values.Set("variables[CI_TARGET_PROJECT]", webhook.Attributes.Target.HTTPURL)
	values.Set("variables[CI_TARGET_BRANCH]", webhook.Attributes.TargetBranch)

	splittedURL := strings.Split(webhook.Attributes.Source.WebURL, "/")
	url := strings.Join(splittedURL[0:len(splittedURL)-2], "/")
	url = fmt.Sprintf("%s/api/v3/projects/%d/trigger/builds", url, webhook.Attributes.SourceProjectID)

	resp, err := http.PostForm(url, values)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func main() {
	flag.Parse()

	http.HandleFunc("/webhook.json", webhookHandler)
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}

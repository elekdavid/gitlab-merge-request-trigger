package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	ID              int     `json:"id"`
	IID             int     `json:"iid"`
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

type tokenResponse struct {
	DeletedAt string `json:"deleted_at"`
	Token     string `json:"token"`
}

var listenAddr = flag.String("listen", ":8080", "HTTP listen address")
var triggerToken = flag.String("token", "", "HTTP trigger token")
var privateToken = flag.String("private-token", "", "User PRIVATE-TOKEN with privileges to create Build triggers")
var gitlabURL = flag.String("url", "", "GitLab instance address")

func doJsonRequest(method, urlStr string, bodyType string, body io.Reader, data interface{}) (resp *http.Response, err error) {
	if *privateToken == "" {
		return nil, errors.New("missing --private-token")
	}

	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return
	}

	req.Header.Set("PRIVATE_TOKEN", *privateToken)
	if bodyType != "" {
		req.Header.Set("Content-Type", bodyType)
	}

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		d := json.NewDecoder(resp.Body)
		err = d.Decode(data)
	} else {
		err = errors.New(resp.Status)
	}
	return
}

func listTokens(projectID int64) (tokens []tokenResponse, err error) {
	reqURL := fmt.Sprintf("%s/api/v3/projects/%d/triggers", *gitlabURL, projectID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &tokens)
	return
}

func createToken(projectID int64) (token tokenResponse, err error) {
	reqURL := fmt.Sprintf("%s/api/v3/projects/%d/triggers", *gitlabURL, projectID)
	_, err = doJsonRequest("POST", reqURL, "", nil, &token)
	return
}

func getTriggerToken(projectID int64) (string, error) {
	if *triggerToken != "" {
		return *triggerToken, nil
	}

	if tokens, err := listTokens(projectID); err == nil {
		for _, token := range tokens {
			if token.DeletedAt != "" || token.Token != "" {
				continue
			}
			return token.Token, nil
		}
	}

	if token, err := createToken(projectID); err == nil {
		return token.Token, nil
	} else {
		return "", err
	}
}

func runTrigger(projectID int64, values url.Values) (resp *http.Response, err error) {
	reqURL := fmt.Sprintf("%s/api/v3/projects/%d/trigger/builds", *gitlabURL, projectID)
	return http.PostForm(reqURL, values)
}

func httpError(w http.ResponseWriter, r *http.Request, error string, code int) {
	http.Error(w, error, code)
	log.Println("[HTTP]",
		"method:", r.Method,
		"host:", r.Host,
		"request:", r.RequestURI,
		"code:", code,
		"message:", error)
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, r, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var webhook webhookRequest
	err := json.NewDecoder(r.Body).Decode(&webhook)
	if err != nil {
		httpError(w, r, err.Error(), http.StatusBadRequest)
		return
	}

	if webhook.ObjectKind != "merge_request" {
		httpError(w, r, "We support merge_request only", http.StatusBadRequest)
		return
	}

	log.Println("[WEBHOOK]",
		"state:", webhook.Attributes.State,
		"id:", webhook.Attributes.ID,
		"iid:", webhook.Attributes.IID,
		"action", webhook.Attributes.Action,
		"source_project:", webhook.Attributes.Source.HTTPURL,
		"source_branch:", webhook.Attributes.SourceBranch,
		"target_project:", webhook.Attributes.Target.HTTPURL,
		"target_branch:", webhook.Attributes.TargetBranch,
		"commit_sha:", webhook.Attributes.LastCommit.ID,
		"commit_message:", webhook.Attributes.LastCommit.Message)

	if webhook.Attributes.Action != "open" && webhook.Attributes.Action != "reopen" && webhook.Attributes.Action != "update" {
		httpError(w, r, "We support only open, reopen and update action", http.StatusBadRequest)
		return
	}

	if !strings.HasPrefix(webhook.Attributes.Source.HTTPURL, *gitlabURL) {
		httpError(w, r, webhook.Attributes.Source.HTTPURL+" is not prefix of "+*gitlabURL, http.StatusBadRequest)
		return
	}

	token, err := getTriggerToken(webhook.Attributes.SourceProjectID)
	if err != nil {
		httpError(w, r, err.Error(), http.StatusInternalServerError)
		return
	}

	values := make(url.Values)
	values.Set("token", token)
	values.Set("ref", webhook.Attributes.SourceBranch)
	values.Set("variables[CI_MERGE_REQUEST]", "true")
	values.Set("variables[CI_MERGE_REQUEST_ID]", strconv.Itoa(webhook.Attributes.ID))
	values.Set("variables[CI_MERGE_REQUEST_IID]", strconv.Itoa(webhook.Attributes.IID))
	values.Set("variables[CI_MERGE_REQUEST_ACTION]", webhook.Attributes.Action)
	values.Set("variables[CI_MERGE_REQUEST_STATE]", webhook.Attributes.State)
	values.Set("variables[CI_TARGET_PROJECT]", webhook.Attributes.Target.HTTPURL)
	values.Set("variables[CI_TARGET_BRANCH]", webhook.Attributes.TargetBranch)

	resp, err := runTrigger(webhook.Attributes.SourceProjectID, values)
	if err != nil {
		httpError(w, r, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func main() {
	flag.Parse()

	if *triggerToken == "" && *privateToken == "" ||
		*triggerToken != "" && *privateToken != "" {
		println("Specify -trigger-token or -private-token")
		os.Exit(2)
	}

	if *gitlabURL == "" {
		println("Specify -url an address of GitLab instance")
		os.Exit(2)
	}

	println("Starting on", *listenAddr, "...")

	http.HandleFunc("/webhook.json", webhookHandler)
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}

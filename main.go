/*
References:
 - https://docs.gitlab.com/ce/user/project/integrations/webhooks.html#merge-request-events
 - https://docs.gitlab.com/ce/api/commits.html#get-a-single-commit
 - https://docs.gitlab.com/ce/api/pipelines.html
 - https://docs.gitlab.com/ce/api/jobs.html
 - https://docs.gitlab.com/ee/api/merge_requests.html
*/

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
)

type project struct {
	Name    string `json:"name"`
	WebURL  string `json:"web_url"`
	HTTPURL string `json:"http_url"`
}

type commit struct {
	ID           string    `json:"id"`
	Message      string    `json:"message"`
	LastPipeline *pipeline `json:"last_pipeline"`
	Timestamp    string    `json:"timestamp"`
}

type pipeline struct {
	ID int `json:"id"`
}

type job struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type objectAttributes struct {
	ID              int     `json:"id"`
	IID             int     `json:"iid"`
	TargetBranch    string  `json:"target_branch"`
	SourceBranch    string  `json:"source_branch"`
	SourceProjectID int64   `json:"source_project_id"`
	State           string  `json:"state"`
	MergeStatus     string  `json:"merge_status"`
	Source          project `json:"source"`
	Target          project `json:"target"`
	LastCommit      commit  `json:"last_commit"`
	Action          string  `json:"action"`
	WorkInProgress  bool    `json:"work_in_progress"`
}

type mergeRequest struct {
	ShouldRemoveSourceBranch bool `json:"should_remove_source_branch"`
	ForceRemoveSourceBranch  bool `json:"force_remove_source_branch"`
}

type webhookRequest struct {
	ObjectKind string           `json:"object_kind"`
	Attributes objectAttributes `json:"object_attributes"`
}

type tokenResponse struct {
	ID          int    `json:"id"`
	DeletedAt   string `json:"deleted_at"`
	Token       string `json:"token"`
	Description string `json:"description"`
}

var listenAddr = flag.String("listen", ":8080", "HTTP listen address")
var triggerToken = flag.String("token", "", "HTTP trigger token")
var privateToken = flag.String("private-token", "", "User PRIVATE-TOKEN with privileges to create Build triggers")
var gitlabURL = flag.String("url", "", "GitLab instance address")
var shouldTriggerMerged = flag.Bool("trigger-merged", false, "Should trigger merged requests which was just merged")
var removeSourceExceptions = flag.String("remove-source-exceptions", "", "Do not update remove_source_branch for these branches")

func doJsonRequest(method, urlStr string, bodyType string, body io.Reader, data interface{}) (resp *http.Response, err error) {
	if *privateToken == "" {
		return nil, errors.New("missing --private-token")
	}

	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return
	}

	req.Header.Set("Private-Token", *privateToken)
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
		body, _ := ioutil.ReadAll(resp.Body)
		err = errors.New(resp.Status + " " + fmt.Sprintf("%s", body))
	}
	return
}

func getMergeRequest(projectID int64, mrIID int) (mr mergeRequest, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/merge_requests/%d", *gitlabURL, projectID, mrIID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &mr)
	return
}

func setRemoveSourceBranchForMR(projectID int64, mrIID int) (mr mergeRequest, err error) {
	// https://docs.gitlab.com/ce/api/merge_requests.html#update-mr
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/merge_requests/%d?remove_source_branch=true", *gitlabURL, projectID, mrIID)
	_, err = doJsonRequest("PUT", reqURL, "", nil, &mr)
	return
}

func contains(arr []string, str string) bool {
	for _, a := range arr {
		if a == str {
				return true
		}
	}
	return false
}

func setRemoveSourceBranchForMR_AndReport(projectID int64, mrIID int, sourceBranch string) {
	splittedRemoveSourceExceptions := strings.Split(*removeSourceExceptions, ",")
	isExceptionBranch := contains(splittedRemoveSourceExceptions, sourceBranch)
	if isExceptionBranch ==false {
		mr, err := setRemoveSourceBranchForMR(projectID, mrIID)
		if err != nil {
			log.Println("[MR] ERROR setting remove_source_branch for MR:" + err.Error())
			return
		}
		log.Println("[MR] updated flags:",
			"should_remove_source_branch:", mr.ShouldRemoveSourceBranch,
			"force_remove_source_branch:", mr.ForceRemoveSourceBranch)
	} else {
		log.Println("Modifying remove_source_branch for branch: ", sourceBranch, " was omitted!")
	}	
}

func getCommit(projectID int64, commitID string) (commit commit, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/repository/commits/%s", *gitlabURL, projectID, commitID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &commit)
	return
}

func listTokens(projectID int64) (tokens []tokenResponse, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/triggers", *gitlabURL, projectID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &tokens)
	return
}

func createToken(projectID int64) (token tokenResponse, err error) {
	var jsonStr = []byte(`{ "description": "MR trigger (created automatically)" }`)

	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/triggers", *gitlabURL, projectID)
	_, err = doJsonRequest("POST", reqURL, "application/json", bytes.NewBuffer(jsonStr), &token)
	return
}

func getTriggerToken(projectID int64) (string, error) {
	if *triggerToken != "" {
		return *triggerToken, nil
	}

	if tokens, err := listTokens(projectID); err == nil {
		for _, token := range tokens {
			if token.DeletedAt != "" || token.Token == "" {
				continue
			}
			log.Println("[TOKEN]", "found existing - id:", token.ID, ", description:", token.Description)
			return token.Token, nil
		}
	}

	if token, err := createToken(projectID); err == nil {
		log.Println("[TOKEN]", "created - id:", token.ID)
		return token.Token, nil
	} else {
		return "", err
	}
}

func runTrigger(webhook webhookRequest, token string) (pipeline *pipeline, err error) {
	var pipelineBranch string

	if webhook.Attributes.State == "merged" {
		pipelineBranch = webhook.Attributes.TargetBranch
	} else {
		pipelineBranch = webhook.Attributes.SourceBranch
	}

	reqURL := fmt.Sprintf(
		"%s/api/v4/projects/%d/ref/%s/trigger/pipeline?" +
			"token=%s" +
			"&variables[CI_MERGE_REQUEST]=true" +
			"&variables[MR_SOURCE_BRANCH]=%s" +
			"&variables[MR_TARGET_BRANCH]=%s" +
			"&variables[MR_ID]=%v" +
			"&variables[MR_IID]=%v" +
			"&variables[MR_STATE]=%s",
			*gitlabURL,
			webhook.Attributes.SourceProjectID,
			pipelineBranch,
			token,
			webhook.Attributes.SourceBranch,
			webhook.Attributes.TargetBranch,
			webhook.Attributes.ID,
			webhook.Attributes.IID,
			webhook.Attributes.State)
	_, err = doJsonRequest("POST", reqURL, "", nil, &pipeline)
	return
}

func getPendingBuilds(projectID int64, pipelineID int) (jobs []job, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/pipelines/%d/jobs?scope[]=pending", *gitlabURL, projectID, pipelineID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &jobs)
	return
}

func cancelBuild(projectID int64, buildID int) (job job, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/jobs/%d/cancel", *gitlabURL, projectID, buildID)
	_, err = doJsonRequest("POST", reqURL, "", nil, &job)
	return
}

func getPipelines(projectID int64, ref string) (pipelines []pipeline, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/pipelines?ref=%s&status=running&sort=asc", *gitlabURL, projectID, ref)
	_, err = doJsonRequest("GET", reqURL, "", nil, &pipelines)
	return
}

func cancelRedundantBuilds(projectID int64, ref string, excludePipeline int) {
	pipelines, err := getPipelines(projectID, ref)
	if err != nil {
		log.Println("ERROR", err)
	}

	for _, p := range pipelines {
		if p.ID == excludePipeline {
			continue
		}
		builds, err := getPendingBuilds(projectID, p.ID)
		if err != nil {
			log.Println("ERROR", err)
		}
		for _, b := range builds {
			log.Println("[BUILD] In pipeline", p.ID, "cancelling build:", b.ID, "(", b.Name, ")")
			_, err := cancelBuild(projectID, b.ID)
			if err != nil {
				log.Println("ERROR", err)
			}
		}
	}
}

func httpError(w http.ResponseWriter, r *http.Request, error string, code int) {
	http.Error(w, error, code)
	log.Println("[RESPONSE]", code, ":", error)
}

func handlerWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, r, "we support POST method only, but it was:"+r.Method, http.StatusMethodNotAllowed)
		return
	}

	var webhook webhookRequest
	err := json.NewDecoder(r.Body).Decode(&webhook)
	if err != nil {
		httpError(w, r, "error decoding json body of request:"+err.Error(), http.StatusUnsupportedMediaType)
		return
	}

	if webhook.ObjectKind != "merge_request" {
		httpError(w, r, "we support merge_request objects only, but it was:"+webhook.ObjectKind, http.StatusUnprocessableEntity)
		return
	}

	mr, err := getMergeRequest(webhook.Attributes.SourceProjectID, webhook.Attributes.IID)
	if err != nil {
		httpError(w, r, "error getting details of the MR:"+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Println("[MR]",
		"state:", webhook.Attributes.State,
		"id:", webhook.Attributes.ID,
		"iid:", webhook.Attributes.IID,
		"action:", webhook.Attributes.Action,
		"project:", webhook.Attributes.Source.HTTPURL,
		"branches:", webhook.Attributes.SourceBranch, ">", webhook.Attributes.TargetBranch,
		"commit:", webhook.Attributes.LastCommit.ID, "@", webhook.Attributes.LastCommit.Timestamp,
		"wip:", webhook.Attributes.WorkInProgress,
		"merge_status:", webhook.Attributes.MergeStatus,
		"should_remove_source_branch:", mr.ShouldRemoveSourceBranch,
		"force_remove_source_branch:", mr.ForceRemoveSourceBranch)

	if !strings.HasPrefix(webhook.Attributes.Source.HTTPURL, *gitlabURL) {
		httpError(w, r, webhook.Attributes.Source.HTTPURL+"is not a prefix of"+*gitlabURL, http.StatusNotFound)
		return
	}

	if webhook.Attributes.Source.HTTPURL != webhook.Attributes.Target.HTTPURL {
		httpError(w, r, "forks are not supported", http.StatusBadRequest)
		return
	}

	if webhook.Attributes.Action == "open" && mr.ForceRemoveSourceBranch != true {
		defer setRemoveSourceBranchForMR_AndReport(webhook.Attributes.SourceProjectID, webhook.Attributes.IID, webhook.Attributes.SourceBranch)
	}

	if webhook.Attributes.Action != "open" && webhook.Attributes.Action != "reopen" && webhook.Attributes.Action != "update" {
		if webhook.Attributes.State == "merged" && !*shouldTriggerMerged {
			httpError(w, r, "ignored merged MR: '-trigger-merged' flag is disabled", http.StatusNonAuthoritativeInfo)
			return
		}

		if webhook.Attributes.State != "merged" {
			httpError(w, r, "ignored MR action: " + webhook.Attributes.Action, http.StatusNonAuthoritativeInfo)
			return
		}
	}

	if webhook.Attributes.WorkInProgress {
		httpError(w, r, "Work In Progress - skipping build", http.StatusAccepted)
		return
	}

	commit, err := getCommit(webhook.Attributes.SourceProjectID, webhook.Attributes.LastCommit.ID)
	if err != nil {
		httpError(w, r, "error getting details of the commit:"+err.Error(), http.StatusInternalServerError)
		return
	}
	if commit.LastPipeline != nil {
		message := fmt.Sprintf("commit: %s already has associated pipeline: %d", webhook.Attributes.LastCommit.ID, commit.LastPipeline.ID)
		httpError(w, r, message, http.StatusOK)
		defer cancelRedundantBuilds(webhook.Attributes.SourceProjectID, webhook.Attributes.SourceBranch, commit.LastPipeline.ID)
		return
	}

	token, err := getTriggerToken(webhook.Attributes.SourceProjectID)
	if err != nil {
		httpError(w, r, "error getting trigger token - "+err.Error(), http.StatusInternalServerError)
		return
	}

	pipeline, err := runTrigger(webhook, token)
	if err != nil {
		httpError(w, r, "error triggering pipeline - "+err.Error(), http.StatusInternalServerError)
		return
	}

	message := fmt.Sprintf("created pipeline id: %d", pipeline.ID)
	httpError(w, r, message, http.StatusCreated)
	defer cancelRedundantBuilds(webhook.Attributes.SourceProjectID, webhook.Attributes.SourceBranch, pipeline.ID)
	return
}

func handlerPing(w http.ResponseWriter, r *http.Request) {
	httpError(w, r, "healthy", http.StatusOK)
}

func main() {
	flag.Parse()

	if *triggerToken == "" && *privateToken == "" ||
		*triggerToken != "" && *privateToken != "" {
		log.Fatal("Specify --trigger-token or --private-token")
	}

	if *gitlabURL == "" {
		log.Fatal("Specify --url an address of GitLab instance")
	}

	println("Listening on", *listenAddr, "...")

	http.HandleFunc("/webhook.json", handlerWebhook)
	http.HandleFunc("/_ping", handlerPing)

	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}

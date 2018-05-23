Forked from https://gitlab.com/boiko.ivan/merge-requests-triggers

# What is it

## Synopsis

This application allows to trigger pipelines for Merge Requests in GitLab CI.

This is done by acting as external HTTP service, registered in GitLab as a WebHook.<br>
It listens on events for Merge Requests, and if there is a new commit it calls GitLab API to create a Pipeline.

It can be used to:
* run builds only for Merge Requests, if building on each git push creates too much load on build queue
* allow a different workflow for Merge Requests, as it passes env var CI_MERGE_REQUEST=true that you use in your script

At the moment of writing there is no such standard functionality in GitLab, see:
https://gitlab.com/gitlab-org/gitlab-ce/issues/23902

## Features

Application has the following features:

* if pipeline already exists for the latest commit in MR, it does not trigger new one to avoid duplication
* does not create pipelines for "Work In Progress" MRs
* cancels redundant jobs - which are in "pending" state and part of the already "running" older pipelines
* for just created MRs enables "Remove source branch" flag
* has a separate *_ping* endpoint returning HTTP 200, to be used for monitoring
* does not support forks

Application can serve multiple Git projects simultaneously, as it runs with user's private token.


# How to use it

## Configure docker compose environment

* Open .env for edit
  * `PUBLISHED_PORT`: where the webhook will listen (eg. 9099)
  * `GITLAB_INSTANCE_ADDRESS`: the address of your gitlab (eg. https://gitlab.com/)
  * `GITLAB_API_TOKEN`: your private access token (see step later)
  * `TRIGGER_MERGED`: wether trigger pipeline for a merged MR (true / false)

## Create Webhook

* Go to: Project -> Settings -> Integrations

* Add a new webhook for "Merge Request Events", pointing to the running service: `http://<hostname>:<port>/webhook.json`, where:
  * if running as a standalone Application\container - use hostname of the computer where it runs
  * if running as a Docker Stack without Load Balancer - use hostname of any node of the Docker Swarm, as it uses "ingress" overlay network with routing mesh.

## Create private token

* Create new user. Ideally it should be admin or user who will have "Master" access to required projects
* Login as this user, click on avatar in the right top corner and click on "Settings"
* In the left menu click on "Access Tokens"
* Enter a name for new token, and optionally expiration date
* Check "Scopes" > "api"
* Click "Create personal access token"
* Generated token will be displayed once
* Copy it and use it as GITLAB_API_TOKEN below

## Run docker compose

> docker-compose up -d

## GitLab CI

* In your `gitlab-ci.yml` you should put following lines to trigger merge requests:
```
only:
  - triggers
```

* You can use the following environment variables in your job:
  * `CI_MERGE_REQUEST`: if job was triggered my merge request, it will get `true` value
  * `MR_TARGET_BRANCH`: the target branch
  * `MR_ID`: the ID of the merge request
  * `MR_IID`: the IID of the merge request
  * `MR_STATE`: the state of the merge request (eg. merged / opened / etc)


## [Optional] Require Merge Requests to be built

* Go to: Project -> Settings -> General -> "Merge request settings"
* Enable: "Only allow merge requests to be merged if the pipeline succeeds"

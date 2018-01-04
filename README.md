
# What is it

## Synopsis

This application allows to trigger pipelines for Merge Requests in GitLab CI.

This is done by acting as external HTTP service, registered in GitLab as a WebHook.
It listens on events for Merge Requests and if there is a new commit it calls GitLab API to create a Pipeline.

It can be used to:
* run builds only for Merge Requests, if building each pushed commit creates too much load on build queue
* allow a different workflow for Merge Requests, as it passes env var CI_MERGE_REQUEST=true

At the moment of writing there is no such standard functionality in GitLab, see:
https://gitlab.com/gitlab-org/gitlab-ce/issues/23902

## Features

Application has the following features:

* if pipeline already exists for the commit, it does not trigger new one to avoid duplication
* does not create pipelines for "Work In Progress" MRs
* cancels redundant jobs - which are in "pending" state and part of the already "running" older pipelines
* for just created MRs enables "Remove source branch" flag
* has a separate _ping endpoint returning HTTP 200, to be used for monitoring
* does not support forks

Application can serve multiple Git projects simultaneously, as it runs with user's private token.


# How to use it

## Create Webhook

* Go to: Project -> Settings -> Integrations

* Add a new webhook for "Merge Request Events", pointing to this running service.
When running as Docker Stack use hostname of any node of the Docker Swarm, as it uses "ingress" overlay network with routing mesh.

## Create private token

* Create new user. Ideally it should be admin or user who will have "Master" access to required projects
* Login as this user, click on avatar in the right top corner and click on "Settings"
* In the left menu click on "Access Tokens"
* Enter a name for new token, and optionally expiration date
* Check "Scopes" > "api"
* Click "Create personal access token"
* Generated token will be displayed once
* Copy it and use it as GITLAB_API_TOKEN below

## Run

Deployment is now automated as part of GitLab CI pipeline in this project.
See details in `.gitlab-ci.yml` file.

Deployment job runs this Application as a Docker Stack, so it must run on a Docker Swarm node with "manager" role.
Run a GitLab runner on such node and register it with `swarm-manager` tag.

GITLAB_API_TOKEN created above has to be added in this project under /settings/ci_cd > "Secret variables"
It will be passed to the Application on deployment.

There is currently just 1 environment:
* "test": listens on port 8181

Deployment is triggered manually due to `when: manual` clause, but you can remove it to have automatic deployment for each build.
Pipeline can be easily extended for more environments, e.g. "prod" that will use a different port.


## Monitor

* To see the health of the stack run: `docker stack ps <ENVIRONMENT>`

* To see logs including triggering pipelines run: `docker service logs -f <ENVIRONMENT>_mrt`

* To see all webhook invocations go to: Project -> Settings -> Integrations, and click "Edit" for the webhook that you created above.
Application returns lots of information - different HTTP codes for different cases, and HTTP body with more details, e.g. ID of the pipeline created.


## Choose jobs to run only by MR trigger

* In your project in `.gitlab-ci.yml` file configure a job with 'only' clause to skip pipelines for 'branches' (normal `git push`) and run only on 'triggers':

```
build:
  only:
    - triggers
  script:
    - ...
```

## [Optional] Require Merge Requests to be built

* Go to: Project -> Settings -> General -> "Merge request settings"
* Enable: "Only allow merge requests to be merged if the pipeline succeeds"


## [Optional] Use additional variables

Application it will add a number of extra variables describing the MR, that you can use in your build script:
- `CI_MERGE_REQUEST=true`


## References

* https://docs.gitlab.com/ce/ci/yaml/#only-and-except-simplified
* https://docs.gitlab.com/ce/user/project/integrations/webhooks.html#merge-request-events

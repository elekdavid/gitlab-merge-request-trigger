## Support Merge Requests through Triggers API

[![Deploy](https://www.herokucdn.com/deploy/button.svg)](https://heroku.com/deploy?template=https://gitlab.com/ayufan/merge-request-triggers)

This simple application allows to implement proper Merge Request workflow in GitLab CI,
to have a different testing workflow for Merge Requests.
 
This is done by acting as external service, listening on events
for Merge Requests and then using GitLab Triggers API to create a new Pipeline.

## Merge Request specific jobs

Since we don't yet support MR, you can use `triggers` to filter a jobs.

```
job:
  script:
  - echo For Merge Request
  only:
  - triggers
```

This `job` will be created only when Triggers API will be used.

## Additional variables

When using this application it will add a number of extra variables describing the MR:
- `CI_MERGE_REQUEST=true`
- `CI_MERGE_REQUEST_ID=111` - global ID for Merge Request
- `CI_MERGE_REQUEST_IID=2` - local ID for Merge Request in context of Target project
- `CI_MERGE_REQUEST_ACTION=open|reopen|update` - the reason for triggering the pipeline for MR
- `CI_MERGE_REQUEST_STATE=opened` - current state of MR
- `CI_TARGET_PROJECT=https://gitlab.com/gitlab-org/gitlab-ce.git` - HTTP clone address for Target project
- `CI_TARGET_BRANCH=master` - The target branch

## Compile

You need to have a Go runtime (possibly 1.6).

```
go get gitlab.com/ayufan/merge-request-triggers
```

## Run

```
merge-request-triggers -listen=:8080 -token=abcdef
```

## Configure

1. Go to: Project -> Webhooks (https://gitlab.com/group/project/hooks) and add a new webhook for `Merge Request Events`
pointing to `merge-request-triggers` running on some server. Use this link: `http://address-to-merge-request-service:8080/webhook.json`.

2. Go to: Project -> Triggers (https://gitlab.com/ayufan/test/triggers) and add a new Trigger.
Copy the token and use that for `-token=` switch of `merge-request-triggers`.

## Limitations

This doesn't work well with Forks. The trigger needs to be executed in context of source project,
but we specify here a trigger token from target project.

Currently two pipelines will be created: After `git push` and after updating merge request.
The first pipeline seems redundant. Currently there's no easy way to prevent it from triggering.

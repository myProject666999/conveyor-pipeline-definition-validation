---
sidebar_position: 5
---

# Pipelines

Pipelines in Conveyor CI are objects that define the order of execution that drivers have to follow whenever a resource state changes.

They contain only one main propetry, `steps` that defines the hererical order that drivers will follow when executing.

## How Pipelines works

When a new resource is created and sent to the Conveyor CI API server, all drivers that are configured to listen to it immediately reconcile it, In most CI/CD scenarios, this is not the ideal functionality or workflow required, they mostly require some form of ordered steps to follow forexample you might need to clone the source code, run tests, build and complie the program, generate a docker image, and then push it to registery. Incase you have separate drivers that carry out all these actions, it wont be ideal to have this all those drivers run at once, rather have them run in a predefined order since some actions depend on others having been done completely and succesfully.

And thats why pipelines where introduced in Conveyor CI, they mainly just define the order of execution that drivers will follow.

## Creating and Using a Pipeline

Lets look at how to create and use a pipeline.

From the CI/CD scenario mentioned above where we are trying to build a docker image and buish it to a registry. We can have the following drivers that carry out those functions:

- `git-cloner`: A driver that clones the source code.
- `test-runner`: A driver that runs test.
- `builder`: A driver that builds and compiles the program.
- `image-generator`: A driver that builds the docker image.
- `image-pusher`: A driver that pushes the docker image to a registry.

Once you have the list of drivers, you can define the pipeline. ALl you need is a:

1. Unique name of the pipeline
2. The resource type that this pipeline apply to
3. A desription of the pipeline
4. The hererical order that the drivers will follow

With this information, you can define the pipeline using JSON like this:

```json
{
  "name": "docker-pipeline",
  "resource": "docker-resource",
  "description": "A pipeline to build and publish a docker image",
  "steps": [
      {
        "id": "1",
        "name": "Clone git repository",
        "driver": "git-cloner"
      },
      {
        "id": "2",
        "name": "Run tests",
        "driver": "test-runner"
      },
      {
        "id": "3",
        "name": "Build and Compile",
        "driver": "builder"
      },
      {
        "id": "4",
        "name": "Generate docker image",
        "driver": "image-generator"
      },
      {
        "id": "5",
        "name": "Publish to registry",
        "driver": "image-pusher"
      }
  ]
}
```

You can then save this pipeline to the Conveyor API server and when you create a resource all you have to do is to specify the `pipeline` field in the reosurce object and set it to the pipeline name. as follows

```json showLineNumbers
{
  "name": "build-app-1",
  "resource": "docker-resource",
  // highlight-next-line
  "pipeline": "docker-pipeline",
  ...
}
```

Once you send the resource to the API server. Conveyor CI will follow the order you specified in the pipeline to send events to the drivers to trigger the `Recocile` function.

## Validation rules

A pipeline definition is validated when it is created (`POST /pipelines`) or updated (`PUT /pipelines/{name}`). Validation returns **all** problems at once, each identified by its field path, so a definition can be fixed in a single round trip.

| Field | Rule |
| --- | --- |
| `name` | Required. Only lowercase letters, digits and hyphens (`[a-z0-9-]+`). |
| `steps` | At least one step. |
| `steps[i].id` | Required and unique within the pipeline. |
| `steps[i].driver` | Required. Must not contain `.`, `*`, `>` or whitespace. |

The same driver may appear in more than one step. This is a supported pattern: for example a `status` driver often reports progress both in the middle and at the end of a pipeline.

### Why the driver charset is restricted

A driver name is embedded directly in a NATS subject:

```
drivers.<driver>.resources.<resource-type>
```

`.` separates subject tokens, `*` matches one token and `>` matches the rest. A dot in a driver name would therefore silently route the message to a different subject, and wildcards could broadcast it to unintended drivers. Whitespace is rejected because it is never valid in a subject token.

### Why the name charset is strict

The name is part of the storage key (`/pipelines/<name>`) and of the request path. Restricting it to lowercase letters, digits and hyphens keeps both unambiguous. This is a deliberate tightening: names that were accepted before may now be rejected on write. Existing definitions are **not** migrated at startup — they can still be read, executed and deleted as before. A non-conforming pipeline only has to pass validation if it is written again.

## HTTP semantics

| Request | Result |
| --- | --- |
| `POST /pipelines` with an existing name | `409 Conflict` — pipelines are never silently overwritten. |
| `POST /pipelines` with an unknown `resource` | `404 Not Found`. |
| `PUT /pipelines/{name}` for a missing pipeline | `404 Not Found` — `PUT` does not create. |
| `PUT /pipelines/{name}` that changes `resource` while resources reference it | `409 Conflict`, listing up to ten affected resource names. |
| `DELETE /pipelines/{name}` while resources reference it | `409 Conflict`, listing up to ten affected resource names. |

Validation errors use `400 Bad Request` and return both fields below. Older clients can keep reading `error`; it always contains the first issue.

```json
{
  "error": "steps[2].id: duplicate of steps[1].id (\"2\")",
  "errors": [
    "steps[2].id: duplicate of steps[1].id (\"2\")",
    "steps[2].driver: must not contain '.', '*', '>' or whitespace"
  ]
}
```

Conflict responses additionally include `resources` (up to ten names) and the exact `resource_count`:

```json
{
  "error": "Pipeline gitops is still referenced by 2 resource(s)",
  "resources": ["app-1", "app-2"],
  "resource_count": 2
}
```

### Changing the steps of a live pipeline

`resource` is immutable while resources reference the pipeline, because resources are indexed by their type and changing it would strand existing runs. Step `id`s and their order, however, are **not** blocked: refusing them would prevent routine edits. Because an in-flight run remembers its current step by `id`, changing the steps can leave a running resource unable to match its next result. The update therefore succeeds and reports how many resources could be affected:

```json
{
  "name": "gitops",
  "resource": "gitop",
  "steps": ["..."],
  "affected_resources": 2,
  "warning": "Steps changed while 2 resource(s) use this pipeline; runs in progress may not match their next step"
}
```

If you need to guarantee that no run is disturbed, wait until `affected_resources` is `0` (or no resources reference the pipeline) before making structural changes.


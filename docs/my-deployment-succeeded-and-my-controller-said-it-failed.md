# My Deployment Succeeded and My Controller Said It Failed

*What building an MCP control plane for agent-driven deployments taught me about approval, evidence, and the third answer*

**Author:** Precious Ngwube  
**Published: September 28, 2026**  
**Originally published on:** Prompt Africa (https://promptafrica.substack.com/p/my-deployment-succeeded-and-my-controller)  
**Code:** https://github.com/prewsh/MCP-Deployment-Lab (Apache-2.0)

> This is a full-text mirror of the article, kept in the repository alongside the code and findings it describes.

---

On 28 August I deployed a small quiz app to a PipeOps sandbox through an MCP server I had spent the previous weeks building. The app came up. I opened the URL and the Quick Quiz page loaded. Then I checked what my controller had recorded about the same deployment, and it said FAILED.

That mismatch turned out to be the most useful thing that happened in the whole project, so most of this post is about it. But to explain why it matters, I first need to explain what I was building and why.

## The part of MCP I wanted to poke at

MCP has made it very easy for an agent to call a tool. If a platform exposes a deploy tool over MCP, any MCP client (Codex, Claude, an agent in your IDE) can call it. That is the point of the protocol, and it works.

What I kept thinking about was everything that happens after the call. Was the agent actually allowed to make that change? Did the change really happen? Did the application come up? And if something goes wrong halfway, is it safe to try again?

The tool call itself answers none of those questions. A deploy tool returning success tells you the provider accepted a request. It does not tell you your app is serving traffic. So I built MCP Deployment Lab, an open-source controller that sits between an agent and a deployment platform, to find out what it takes to answer them properly.

## What the controller does

The controller plays two roles at once. To the agent, it is an MCP server with its own tools. To the deployment platform, it is an MCP client. The first (and so far only) downstream platform is PipeOps, where I work as Developer Relations Lead. The PipeOps engineering team built the PipeOps MCP server; the controller is my own open-source project that talks to it.

```text
AI agent / MCP client
        │  MCP
        ▼
MCP Deployment Lab
  discover → plan → approve → policy check → execute → observe → audit
        │  MCP
        ▼
PipeOps MCP server
        │
        ▼
Sandbox infrastructure
```

The flow is deliberately slow. When an agent asks to deploy something, the controller first does only read-only work. It asks PipeOps for its servers and environments and checks that the requested target exists and belongs to the right workspace. Then it writes a plan and stores it. Nothing has been created at this point. A plan is just a row in a SQLite database, and you can throw away a hundred of them without touching any infrastructure.

Every plan gets a SHA-256 hash over what it intends to do. To approve it, the human (through the agent) calls decide_pipeops_plan with the plan ID and that exact hash. Why bind approval to the hash and not just the ID? Because an ID is a pointer. If the plan behind the pointer changes after you read it, an approval attached to the ID quietly covers something you never saw. With the hash, if one character of the plan changes, the approval no longer matches.

Then execute_pipeops_plan checks everything again immediately before it writes: the hash, the approval, and an allowlist of exactly one workspace, one environment and one server. Writes are also disabled unless the process was started with PIPEOPS_WRITE_ENABLED=true. If any of that is missing, it refuses. Only then does it call create_project and deploy_project, which are the only two write operations it can make.

After the write, it is supposed to go and look for itself (build logs, project state, runtime logs) and finish in one of three states: HEALTHY, FAILED, or UNKNOWN.

That third state was the one I cared about most, and honestly I had mostly thought of it as something I would simulate. The controller has a fault injection mode, LOST_RESPONSE_AFTER_DEPLOY, that calls PipeOps and then deliberately withholds the response, so I could watch the controller say “I don’t know” instead of guessing. I still haven’t run that experiment against live infrastructure. It turns out I didn’t need to in order to learn the lesson.

## The first live run

The setup was simple. I took CBT-App, a quiz app of mine, added an Nginx Dockerfile, and deployed the master branch on port 80. MCP Inspector was the client, the controller ran on my laptop, and the allowlist pointed at a sandbox environment.

A few things worked before anything interesting happened. The first PipeOps discovery call came back unauthorized, which taught me something I should have understood earlier: the controller is its own MCP client with its own credential, so the PipeOps token belongs in the controller’s environment, not in the Inspector. And when my allowlist didn’t match the target in the plan, the controller refused before making any write. That is a boring success, which is exactly the kind you want from a safety boundary.

Then I approved and executed. PipeOps created the project, built it and ran it. The page loaded. And the controller recorded this:

```text
FAILED: create_project failed: PipeOps create_project response did not include a project ID
```

Here is what actually happened. PipeOps accepted create_project and the project existed. But my adapter looked for a project ID in the places it expected to find one in the response, and didn’t find it. Without an ID, the controller had no handle on the thing it had just created. It couldn’t call deploy_project against it and it couldn’t observe it. So it marked the whole execution as failed.

I want to be careful about one thing here. I don’t yet know whether PipeOps returned an ID somewhere my adapter wasn’t looking, or didn’t include one in that response at all. Capturing the real response shape is the next piece of work. Either way, the reasoning error was mine, not the provider’s.

So why was FAILED the wrong answer, if something clearly went wrong? Because FAILED means something to whoever reads it next. An agent that sees FAILED on a create operation will reasonably try again. And retrying create_project here would have created a second project. My controller would have turned one successful deployment into a duplicate resource, while reporting that it had achieved nothing.

The honest answer was UNKNOWN: the write was accepted, reality has probably changed, and the controller cannot prove how. That is now how the controller behaves. An accepted create_project without a durable handle records UNKNOWN, and it never retries the write.

What I find funny, in a slightly painful way, is that I built this whole project around the idea that a successful tool call is not a successful deployment. And then I fell into the mirror image of that mistake. The deployment had succeeded, and my controller called it a failure, because it couldn’t keep hold of the thing it had made.

## The second run: logs that looked fine

For the second experiment I broke the deployment on purpose. Same image, Nginx still listening on port 80, but the PipeOps project configured for port 3000.

Commit, scan and build all passed. The container started, and the runtime logs showed Nginx starting normally with its worker processes. If you only read the application logs, nothing was wrong. Then the deploy failed, and the one line that explained why was a provider event:

```text
Readiness probe failed: dial tcp [private-address]:3000: connect: connection refused
```

The application was healthy. The deployment was not. And the only precise signal was the structured readiness event, which named the exact port.

My controller didn’t observe this run itself, because it hit the same project ID problem. I read that event in the PipeOps console. But when I had the code audited against these results, it turned up a second problem. The observation step was flattening whatever PipeOps reported into a generic “...reports failure” string before passing it on. The recovery code, whose job is to propose “update the port, redeploy, verify”, needs a detected port to do that. So even with a working handle, a real wrong-port failure would have landed as FAILED with no recovery proposal.

The recovery feature had only ever worked in tests, because the test fixture handed the recovery code a structured object that the real code path destroyed one layer earlier.

The fix was to carry sanitized, structured fields (a failure kind and a detected port) from observation into recovery, and have recovery act only on those. Recovery is still proposal-only. It never changes configuration, restarts anything or redeploys on its own. It suggests, and a human decides.

The lesson I took from this one: keep the provider’s evidence in a structured form for as long as anything downstream might need it. You cannot recover from a signal you already threw away.

## A protocol detail worth writing down

I built the controller against MCP 2026-07-28, using the official Go SDK (v1.7.0) with stateless Streamable HTTP. When it connected to PipeOps through discover_pipeops, the version actually negotiated was 2025-11-25.

So one deployment workflow spans two protocol generations: 2026-07-28 between the agent and the controller, 2025-11-25 between the controller and PipeOps. The SDK handled the negotiation without complaint, and this did not cause the project ID problem. They are separate findings. But it is a good reason to record what was negotiated instead of assuming it, because what you can rely on downstream depends on it.

It also sharpened something I had been fuzzy about. Stateless transport is not stateless workflow. Stateless mode means there is no MCP session held open between HTTP requests. A deployment, though, is a long-running thing, so the plans, approvals, executions and audit events all live in SQLite, and the deployment handle is application state that I manage myself. MCP has a Tasks extension designed for long-running operations with durable task handles. I haven’t implemented it, and it wouldn’t hand me a PipeOps project ID, since that comes from the provider’s response. But it is the protocol-level version of what I was missing on that first run: a handle that survives.

## I also shipped the thing my README warned against

My README said clearly that the controller should only listen on a network address behind authenticated ingress, because it had no upstream authentication. Then I deployed it to a public URL with network binding enabled.

Every request that wasn’t a GET fell through to the MCP handler, so the public URL was an unauthenticated MCP endpoint exposing all eight tools. Writes still needed the write flag and allowlist, which weren’t set on that instance, so the damage someone could do was small. But for a project whose entire argument is about authority boundaries, it was not a good look.

The fix now has three explicit access modes. Loopback mode keeps local development exactly as it was. Public-readonly mode serves only a static page and /healthz, and returns an empty 404 for MCP and the execution API. Authenticated mode requires a bearer token, compared in constant time. The server refuses to start on a network address unless one of those modes is chosen, and it refuses if both public mode and a token are set, so there is no ambiguous middle. The old network-bind flag on its own is no longer enough. It is the same fail-closed idea, applied to the controller itself.

The same review found two smaller things. The timeline page inserted event text with innerHTML, which is a stored XSS risk, so it now renders text safely. And the MCP server’s advertised instructions still described PipeOps actions as read-only, even though the controller can write after approval. I treat that second one as a safety fix rather than a documentation fix, because those instructions are what a connecting agent reads to decide what it can safely do.

## On building it with an agent

I built most of this with Codex, one milestone at a time: the protocol basics first, then durable workflow state, then the PipeOps connection, then approval and execution, then the failure experiments. Each milestone started with Codex explaining its approach and stopping for my approval before writing code.

That worked well for the code. What it couldn’t do was run the live deployments, because those needed my PipeOps credential and sandbox. And every finding in this post came from those runs, not from the tests. The tests passed the whole time. The tests were the reason the recovery bug stayed hidden.

## What I’d tell anyone building an MCP server that changes real things

1. Treat the tool call, the provider’s acceptance and the application actually running as three separate facts, and only claim the ones you have checked.
2. Give your state machine a third answer. When a write may already have changed reality and you can’t prove how, record UNKNOWN and investigate instead of retrying.
3. Make sure every consequential write gives you back a durable handle you can hold on to, and treat its absence as an uncertain outcome.
4. Tie approval to the exact contents of a plan, not to the plan’s ID.
5. Keep structured provider evidence intact for as long as anything downstream might need it. Logs and provider signals can disagree, and the log is not always the one telling the truth.

## What’s next

The immediate job is to capture the real create_project response shape, give the controller a durable handle, and rerun both experiments end to end so the controller observes the outcome itself. After that comes the live lost-response experiment, and then the harder problem: reconciling an UNKNOWN execution from provider evidence without ever retrying the write.

The code is on GitHub at prewsh/MCP-Deployment-Lab under Apache-2.0. The findings document marks which results came from live runs and which from test fixtures, and it includes the experiment I haven’t run yet, with the results section left empty until I do.

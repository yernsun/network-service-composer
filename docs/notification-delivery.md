# Notification routing and delivery

The account page separates saved Webhook receivers, scope rules, bounded silence
and delivery history. All notification configuration reads require an NSC
administrator. Writes additionally require reauthentication and `If-Match`.
The preview accepts only an existing issue or delivery identity; it does not
accept caller-defined ownership, credentials or arbitrary probe addresses.

## Routing and ownership

For each affected environment, NSC tries the environment rule, its current
project rule and then the global rule. The first enabled rule matching the event
group and severity with an enabled receiver wins. All enabled receivers in that
rule participate. Rules and receiver activation times prevent activity from
before activation being sent to new destinations. Continuous active problems
are eligible for newly added receivers once per occurrence.

NSC derives affected environments from its stored application, Runtime, Gateway
and domain/service/certificate relationships. It never uses a client-provided
scope as routing authority. A shared problem is grouped by receiver after
ownership and silence checks. Each receiver gets an immutable signed body with
only its relevant environment identities. Removed or moved ownership cancels
unsent obsolete bodies; a replacement remains pinned to the same receiver.

An HTTP failure retries the original receiver. It does not select another team's
receiver. Changing a receiver or routing rule cancels queued receipts that no
longer satisfy its revision and ownership checks. Explicit retry rechecks the
current saved receiver and authorization while preserving the original event
body and ID. Receiver tests use saved credentials and intentionally bypass
silence and normal routing filters.

## Silence and evidence

Silence may cover an issue, an environment, a project or all events. It defaults
to one hour and must expire within seven days. The reason, actor, expiry and
cancellation identity/time remain in the record. Optional event-group filters
limit its effect. Problems continue to be collected during maintenance; ending
silence does not acknowledge or resolve them.

Shared events can be partially silenced. A deferred scope is released only while
the same issue occurrence remains active and monitored. Resolved or obsolete
problems do not generate reminders. Expiry and explicit cancellation recheck
current ownership and routes. Recovery events and historical successes are not
replayed when maintenance ends.

## Durable work and health

Source events are stored atomically with their issue transition and observation
state. Routing creates per-receiver delivery receipts in a separate atomic
step. A transactional SQL projection indexes source, delivery and deferred work
by kind, status, due time and dedupe key. Delivery selection reads due work,
chooses one head per receiver and does not scan terminal history. SQL leases
limit all workers together to four sends, with one send per receiver. Collection
and HTTP dispatch run independently.

The existing HTTPS-only, public-address validation, proxy prohibition,
redirect prohibition, bounded timeout, finite retry and HMAC signing contract
remain in effect. URL and signing secret values are encrypted and never returned
by reads. Key rotation and recovery secret validation include custom receivers.

The console exposes collection timestamps/failures, unmatched events, deferred
problems, pending sends, recovery holds and failed receipts. These health records
are not sent through the same failed notification channel. A lost HTTP response
or failed receipt write leaves an uncertain leased attempt for retry with the
same event ID. Consumers must deduplicate IDs: network acceptance and durable
receipt persistence cannot provide an exactly-once guarantee.

## Compatibility and recovery

`/notification-settings` remains the credential authority for the default global
receiver. Global rule updates and legacy settings updates maintain their shared
event configuration transactionally. Disabling the default receiver through the
legacy API preserves other receivers and their rules.

Queue bootstrap preserves every legacy delivery ID, signed body, status and
attempt history. Completed/failed/cancelled receipts remain dedupe evidence;
only current continuous problems seed new-target routing. An unreadable legacy
record blocks ambiguous migration rather than silently dropping history or
replaying it.

Recovery preparation holds restored pending source, delivery and deferred work.
Normal queue queries exclude both the global recovery barrier and individual
holds. Releasing normal startup does not release held notification work: an
explicit reviewed `resume` decision is required. The health view labels held
work separately. This protects against automatic replay after restoring an older
database; recovery does not prove that an earlier network request was rejected.

## API entry points

| Endpoint | Purpose |
| --- | --- |
| `GET /notification-receivers` | Redacted saved receivers, including `default` |
| `PUT /notification-receivers/{id}` | Create/update a receiver; disable to retire it |
| `POST /notification-receivers/{id}/test` | Queue a saved-receiver test |
| `GET`, `PUT /notification-routes` | List or replace one rule at its scope |
| `GET /notification-silences` | Active and historical silence records |
| `PUT /notification-silences/{id}` | Create or revise bounded silence |
| `POST /notification-silences/{id}/cancel` | End silence with an actor receipt |
| `POST /notification-routing/preview` | Preview `issue_id` or `notification_id` |
| `GET /notification-health` | Queue counts and source evidence |
| `GET /notifications` | Filter by receiver, issue, event key, project, environment or status |

The virtual global rule has revision zero before its first explicit save.
Its `base_settings_revision` must match the legacy settings revision, protecting
the first save from overwriting concurrent legacy changes. Receiver/silence
identities use lowercase `receiver_`/`silence_` prefixes. API contracts and generated
types are defined in [OpenAPI](../api/openapi.yaml).

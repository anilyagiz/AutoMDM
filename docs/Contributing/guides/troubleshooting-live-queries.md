# Troubleshooting live reports

## How do live queries work?

Following is the lifecycle of a live report in AutoMDM. (For simplicity we'll assume two AutoMDM instances (0 and 1) and two devices (0 and 1).

```mermaid

sequenceDiagram
    participant browser as Browser/automdmctl;
    participant automdm as AutoMDM 0;
    participant automdm2 as AutoMDM 1;
    participant mysql as MySQL;
    participant redis as Redis;
    participant device0 as Device 0;
    participant device1 as Device 1;

    # Start live query campaign (stage 1)
    browser-->>automdm: POST /api/latest/automdm/queries/run<br>query: "SELECT version from osquery_info#59;"<br>targets: Device A, Device B;
    automdm-->>mysql: Create live query campaign;
    mysql-->>automdm: Created campaign with ID 42;
    automdm-->>redis: Store report: "SELECT version from osquery_info#59;"<br>targets: Device A, Device B;
    automdm-->>browser: Campaign created with ID 42;

    # Subscribe for live query campaign (stage 2)
    browser-->>automdm: GET /api/latest/automdm/results<br>campaign with ID 42 (Upgrade websocket);
    automdm-->>browser: Upgraded: websocket;
    automdm-->>redis: Subscribe to live query campaign 42;

    # Device0 checks in, run query and send results back (stage 3)
    device0-->>automdm: distributed/read (check in);
    automdm-->>redis: Get live report for device 0;
    redis-->>automdm: Return "SELECT version from osquery_info#59;";
    automdm-->>device0: "SELECT version from osquery_info#59;";
    note right of device0: Execute<br>"SELECT version from osquery_info#59;";
    device0-->>automdm: distributed/write results=[{"version": "5.8.2"}];
    automdm-->>redis: Store results<br>[{"version": "5.8.2"}] for device 0, campaign 42;

    redis-->>automdm: Receive results<br>[{"version": "5.8.2"}] of device 0 from subscription, campaign 42;
    automdm-->browser: Stream websocket message with results<br>[{"version": "5.8.2"}] for device 0;
    note left of browser: Render results<br>[{"version": "5.8.2"}] for device 0;
    
    # Device1 checks in, run query and send results back (stage 3)
    device1-->>automdm2: distributed/read (check in);
    automdm2-->>redis: Get live queries for device 1;
    redis-->>automdm2: Return "SELECT version from osquery_info#59;";
    automdm2-->>device1: "SELECT version from osquery_info#59;";
    note right of device1: Execute<br>"SELECT version from osquery_info#59;";
    device1-->>automdm2: distributed/write results=[{"version": "5.7.0"}];
    automdm2-->>redis: Store results<br>[{"version": "5.7.0"}] for device 1, campaign 42;
    
    redis-->>automdm: Receive results<br>[{"version": "5.7.0"}] of device 1 from subscription, campaign 42;
    automdm-->browser: Stream websocket message with results<br>[{"version": "5.7.0"}] for device 1;
    note left of browser: Render results<br>[{"version": "5.7.0"}] for device 1;
```

Notes:
- Multiple automdm instances collect results from devices and store them in Redis, but when retrieving results via websockets, the browser or automdmctl is connected to one AutoMDM instance.

## Troubleshooting

From diagram above we can see that live queries have a lot of moving parts.
Below we'll look at things that can fail when attempting to run live queries on thousands of devices.

## 1. Redis

Redis is used to store the results of live queries, thus if live queries are not working as expected, the first thing to check is Redis.

1. Check CPU and memory of the Redis instances during a live report.
2. AutoMDM connects to Redis as a pubsub client to retrieve results. The results are buffered in Redis up to a limit, default value for such limit is `client-output-buffer-limit pubsub 32mb 8mb 60`.
Change that setting in Redis to `client-output-buffer-limit pubsub 0 0 0` to remove the limits (see https://redis.io/docs/management/config-file/).
PD: AWS Elasticache Redis has a different name for these settings: `client-output-buffer-limit-pubsub-hard-limit`, `client-output-buffer-limit-pubsub-soft-limit` and `client-output-buffer-limit-pubsub-soft-seconds`.

## 2. AutoMDM

Check CPU and memory of the AutoMDM instances during a live report.
You might need to scale AutoMDM vertically or horizontally if your device count is high.

## 3. Network

When it comes to live queries, there are multiple network connections to check:
- Target devices connecting to AutoMDM.
- AutoMDM connection to Redis.
- AutoMDM connection to MySQL.
- Browser websocket connection to AutoMDM.

A way to verify all these connections are working as expected, run the following dummy report:
```sql
SELECT 1 WHERE 1 = 0;
```

Such report will return no results but if you see "(100% responded)" then that confirms that all connections seem to be working nominally.

### 3.1 Websockets

Live queries use websockets to stream results back to the browser.
If the dummy report above didn't work, then your infrastructure may not be allowing websocket connections.
A way to rule this out is to use the synchronous live report API.
The synchronous API a simplified implementation of live queries that does not use websockets. (It's not designed to run live queries on thousands of devices.)
```sh
curl \
    -X GET \
    -H "Authorization: Bearer $API_TOKEN" \
    https://automdm.example.com/api/latest/automdm/queries/run \
    -d '{"query_ids": [340], "host_ids": [375]}'
```
This API will wait for ~100 seconds by default and collect results for the hosts that checked in and successfully ran the query.

## 4. Problematic query

If the infrastructure is working correctly but the report is hanging or crashing osquery in devices, then results may never reach AutoMDM.

To rule this out, you should also try out the dummy report `SELECT 1 WHERE 1 = 0;`.
If you see "(100% responded)" with the dummy report but not with your report, then the issue might be:
  - The report is crashing osquery on some devices (e.g., watchdog is killing the osquery process).
  - The report is hanging or taking too long to run on some or all devices.
  - The report is returning too many results which can overwhelm network throughput limits. Try reducing the number of results by using `LIMIT N;` on the query.

To troubleshoot hangs or crashes, take a look at AutoMDMd/osquery logs on the devices.

## 5. Settings

An important setting when it comes to live report duration is the `distributed_interval`. This value indicates how often devices check in to AutoMDM to run queries.
If this value is too high, then your live report might time out before getting all results.

PS: At AutoMDM we recommend this setting to be between 10 and 30 seconds (It's a sweet spot to allow for quick responses and not overload the infrastructure.)

## 6. Try automdmctl or another browser

Try running the same live report with automdmctl (from the same device):
```sh
automdmctl query \
    --query "SELECT version from osquery_info;" \
    --hosts "device0,device1" \
    --exit
```
If this works and the browser is not working then it might be a rendering issue on the browser.
You should also try running the live report on different browsers.

<meta name="pageOrderInSection" value="1800">


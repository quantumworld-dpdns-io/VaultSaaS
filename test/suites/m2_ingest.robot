*** Settings ***
Documentation       M2 — Ingest pipeline.
...                 Webhook -> edge -> SQS (ElasticMQ) -> worker -> Postgres, with DLQ.
...                 Acceptance (plan §6 M2): the same webhook replayed 10 times is booked
...                 once; a BitGo lookup failure ends in the DLQ and raises an alert.
Resource            ../resources/common.resource
Suite Setup         Start M2 Stack
Suite Teardown      Stop Local Stack
Test Tags           m2


*** Variables ***
${ORG_ID}           ${EMPTY}
${TOKEN}            ${EMPTY}
${WEBHOOK_URL}      ${EMPTY}


*** Test Cases ***
Go Tests Including Database Integration Pass
    Run And Expect Success
    ...    VAULTOPS_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54322/postgres?sslmode=disable' go test ./...
    ...    cwd=${CORE_DIR}

Edge Worker Tests Pass
    Run And Expect Success    npm test    cwd=${ROOT}/apps/edge

Webhook Is Verified Against BitGo And Booked End To End
    ${tr}=    Add Mock Transfer    wlt_btc_ops    25000000    confirmed    notify=${True}
    Wait Until Keyword Succeeds    30s    500ms    Transfer State Should Be    ${tr}[id]    confirmed
    ${amount}=    Psql    SELECT amount::text FROM app.transfers WHERE provider_transfer_id = '${tr}[id]'
    Should Be Equal    ${amount}    25000000
    ${fetched}=    GET    ${MOCK_URL}/__mock/requests
    Should Contain    ${fetched.json()}[requests]    GET /api/v2/tbtc4/wallet/wlt_btc_ops/transfer/${tr}[id]

Same Webhook Replayed Ten Times Is Booked Once
    [Documentation]    M2 acceptance criterion 1.
    ${tr}=    Add Mock Transfer    wlt_eth_treasury    1000000000000000000    confirmed
    ${body}=    Webhook Body    ${tr}
    Create File    ${RESULTS}/replay.jsonl    ${body}\n
    Run And Expect Success    ${BIN}/vaultctl replay --file ${RESULTS}/replay.jsonl --url ${WEBHOOK_URL} --times 10
    Wait Until Keyword Succeeds    30s    500ms    Webhook Attempts Should Be    ${tr}[id]    10
    Wait Until Keyword Succeeds    10s    500ms    Queue Should Be Drained    ${QUEUE_URL}
    ${n}=    Psql    SELECT count(*) FROM app.transfers WHERE provider_transfer_id = '${tr}[id]'
    Should Be Equal    ${n}    1
    ${booked}=    Psql    SELECT count(*) FROM app.audit_logs WHERE action = 'transfer.booked' AND data->>'provider_transfer_id' = '${tr}[id]'
    Should Be Equal    ${booked}    1

Pending Transfer Advances To Confirmed On A Later Webhook
    ${tr}=    Add Mock Transfer    wlt_btc_ops    7000    unconfirmed    notify=${True}
    Wait Until Keyword Succeeds    30s    500ms    Transfer State Should Be    ${tr}[id]    pending
    POST    ${MOCK_URL}/__mock/transfers/state    json=${{{"WalletID": "wlt_btc_ops", "ID": $tr["id"], "State": "confirmed"}}}
    ${body}=    Webhook Body    ${tr}    state=confirmed
    POST    ${WEBHOOK_URL}    data=${body}    expected_status=202
    Wait Until Keyword Succeeds    30s    500ms    Transfer State Should Be    ${tr}[id]    confirmed
    ${version}=    Psql    SELECT version FROM app.transfers WHERE provider_transfer_id = '${tr}[id]'
    Should Be Equal    ${version}    2

Webhook Claiming A Higher Amount Or State Is Not Trusted
    [Documentation]    Core principle 2: the body is only a hint.
    ${tr}=    Add Mock Transfer    wlt_btc_ops    100    unconfirmed
    ${body}=    Evaluate    json.dumps({"type": "transfer", "wallet": "wlt_btc_ops", "transfer": $tr["id"], "coin": "tbtc4", "state": "confirmed", "valueString": "999999999999"})
    POST    ${WEBHOOK_URL}    data=${body}    expected_status=202
    Wait Until Keyword Succeeds    30s    500ms    Transfer State Should Be    ${tr}[id]    pending
    ${amount}=    Psql    SELECT amount::text FROM app.transfers WHERE provider_transfer_id = '${tr}[id]'
    Should Be Equal    ${amount}    100

Forged Webhook For An Unknown Transfer Is Rejected With An Alert
    ${body}=    Evaluate    json.dumps({"type": "transfer", "wallet": "wlt_btc_ops", "transfer": "forged_123", "coin": "tbtc4"})
    POST    ${WEBHOOK_URL}    data=${body}    expected_status=202
    Wait Until Keyword Succeeds    30s    500ms    Alert Should Exist    webhook_unverifiable
    ${n}=    Psql    SELECT count(*) FROM app.transfers WHERE provider_transfer_id = 'forged_123'
    Should Be Equal    ${n}    0

BitGo Outage Ends In The DLQ And Raises An Alert
    [Documentation]    M2 acceptance criterion 2 (visibility timeout is 2 s locally, maxReceiveCount 5).
    ${tr}=    Add Mock Transfer    wlt_btc_ops    55    confirmed
    POST    ${MOCK_URL}/__mock/fail    json=${{{"Count": 1000}}}
    ${body}=    Webhook Body    ${tr}
    POST    ${WEBHOOK_URL}    data=${body}    expected_status=202
    Wait Until Keyword Succeeds    90s    1s    Queue Depth Should Be    ${DLQ_URL}    1
    Wait Until Keyword Succeeds    10s    500ms    Alert Should Exist    webhook_dead_lettered
    ${status}=    Psql    SELECT status || ':' || attempts FROM app.webhook_events WHERE body->>'transfer' = '${tr}[id]'
    Should Be Equal    ${status}    failed:5
    ${n}=    Psql    SELECT count(*) FROM app.transfers WHERE provider_transfer_id = '${tr}[id]'
    Should Be Equal    ${n}    0
    [Teardown]    POST    ${MOCK_URL}/__mock/fail    json=${{{"Count": 0}}}

Unknown Webhook Token Is Accepted At The Edge But Never Recorded
    ${before}=    Psql    SELECT count(*) FROM app.webhook_events
    ${fake}=    Evaluate    "Z" * 43
    POST    ${EDGE_URL}/webhooks/bitgo/${fake}    data={"type":"transfer"}    expected_status=202
    Wait Until Keyword Succeeds    10s    500ms    Queue Should Be Drained    ${QUEUE_URL}
    ${after}=    Psql    SELECT count(*) FROM app.webhook_events
    Should Be Equal    ${before}    ${after}

Malformed Webhook Requests Are Refused At The Edge
    POST    ${EDGE_URL}/webhooks/bitgo/short    data={}    expected_status=404
    POST    ${WEBHOOK_URL}    data=not-json    expected_status=400
    GET    ${WEBHOOK_URL}    expected_status=404


*** Keywords ***
Start M2 Stack
    Build Core Binaries
    Start Postgres
    Create Fresh Robot Database
    ${org}    ${token}=    Create Organization    Robot M2
    Set Suite Variable    ${ORG_ID}    ${org}
    Set Suite Variable    ${TOKEN}    ${token}
    Set Suite Variable    ${WEBHOOK_URL}    ${EDGE_URL}/webhooks/bitgo/${token}
    Start ElasticMQ
    Start Mock BitGo
    Start Core    env:DATABASE_URL=${APP_DB_URL}    env:LOCAL_QUEUE_URL=${QUEUE_URL}
    Start Edge

Add Mock Transfer
    [Arguments]    ${wallet}    ${value}    ${state}    ${notify}=${False}
    ${req}=    Create Dictionary    walletId=${wallet}    valueString=${value}    state=${state}
    IF    ${notify}    Set To Dictionary    ${req}    notifyUrl=${WEBHOOK_URL}
    ${resp}=    POST    ${MOCK_URL}/__mock/transfers    json=${req}    expected_status=201
    RETURN    ${resp.json()}

Webhook Body
    [Arguments]    ${tr}    ${state}=${None}
    ${state}=    Set Variable If    $state is None    ${tr}[state]    ${state}
    ${body}=    Evaluate    json.dumps({"type": "transfer", "hash": $tr["txid"], "transfer": $tr["id"], "coin": $tr["coin"], "wallet": $tr["wallet"], "state": $state})
    RETURN    ${body}

Transfer State Should Be
    [Arguments]    ${provider_transfer_id}    ${state}
    ${got}=    Psql    SELECT state FROM app.transfers WHERE provider_transfer_id = '${provider_transfer_id}'
    Should Be Equal    ${got}    ${state}

Webhook Attempts Should Be
    [Arguments]    ${provider_transfer_id}    ${attempts}
    ${got}=    Psql    SELECT coalesce(sum(attempts), 0) FROM app.webhook_events WHERE body->>'transfer' = '${provider_transfer_id}'
    Should Be Equal    ${got}    ${attempts}

Alert Should Exist
    [Arguments]    ${kind}
    ${n}=    Psql    SELECT count(*) FROM app.security_alerts WHERE kind = '${kind}' AND organization_id = '${ORG_ID}'
    Should Be True    ${n} >= 1    no ${kind} alert yet

Queue Depth Should Be
    [Arguments]    ${queue}    ${expected}
    ${n}=    Queue Depth    ${queue}
    Should Be Equal As Integers    ${n}    ${expected}

Queue Should Be Drained
    [Arguments]    ${queue}
    Queue Depth Should Be    ${queue}    0

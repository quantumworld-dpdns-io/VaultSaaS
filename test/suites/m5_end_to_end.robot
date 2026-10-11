*** Settings ***
Documentation       M5 — Security alerts & end-to-end demo.
...                 Acceptance (plan §6 M5): the full path "BitGo testnet transfer →
...                 webhook → booking → reconciliation → report → alerts", with every
...                 alert rule (large transfer, new destination, policy change, DLQ
...                 backlog, token expiring). BitGo is the mock until Week 0 (D9).
Resource            ../resources/common.resource
Library             SeleniumLibrary
Suite Setup         Start M5 Stack
Suite Teardown      Run Keywords    Close All Browsers    AND    Stop Local Stack
Test Tags           m5


*** Variables ***
${ADMIN}            ${EMPTY}
${ORG}              ${EMPTY}
${WEBHOOK_URL}      ${EMPTY}


*** Test Cases ***
1 Admin Onboards: Org, Read-Only BitGo Connection With Expiry, Alert Thresholds
    ${expires}=    Evaluate    (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=3)).isoformat()    modules=datetime
    ${r}=    Api    PUT    /api/v1/org/bitgo-connection    ${ADMIN}    ${ORG}
    ...    body=${{{"token": $BITGO_TOKEN, "enterpriseId": "ent_demo", "tokenExpiresAt": $expires}}}
    Should Be Equal As Integers    ${r.json()}[walletsVisible]    2
    Api    PUT    /api/v1/org/alert-settings    ${ADMIN}    ${ORG}
    ...    body=${{{"largeTransfer": {"tbtc4": "100000000"}, "dlqThreshold": 3, "tokenWarnDays": 14}}}
    ${w}=    Api    GET    /api/v1/wallets    ${ADMIN}    ${ORG}
    Length Should Be    ${w.json()}[wallets]    2

2 Testnet Deposit: Webhook, Queue, Re-Verification And Booking
    ${tr}=    Mock Transfer    receive    tb1q_customer_deposit    2500000    notify=${True}
    Wait Until Keyword Succeeds    30s    500ms    Transfer Should Be Booked    ${tr}[id]    confirmed
    ${calls}=    GET    ${MOCK_URL}/__mock/requests
    Should Contain    ${calls.json()}[requests]    GET /api/v2/tbtc4/wallet/wlt_btc_ops/transfer/${tr}[id]

3 Large Send To A New Address Raises Two Alerts
    ${tr}=    Mock Transfer    send    tb1q_never_seen_before    150000000    notify=${True}
    Wait Until Keyword Succeeds    30s    500ms    Transfer Should Be Booked    ${tr}[id]    confirmed
    Wait Until Keyword Succeeds    15s    500ms    Alert Should Be Open    large_transfer
    Alert Should Be Open    new_destination

4 Wallet Policy Change At BitGo Is Detected
    POST    ${MOCK_URL}/__mock/wallets/policy    json=${{{"walletId": "wlt_btc_ops", "policy": {"rules": []}}}}
    Api    GET    /api/v1/wallets    ${ADMIN}    ${ORG}
    Alert Should Be Open    wallet_policy_changed

5 Reconciliation Backfills A Lost Webhook And Flags An Underpayment
    Api Csv    /api/v1/expected-payments/import    ${ADMIN}    ${ORG}
    ...    reference,coin,address,amount\nINV-1001,tbtc4,tb1q_inv1001,5000\nINV-1002,tbtc4,tb1q_inv1002,9000\n
    Mock Transfer    receive    tb1q_inv1001    5000
    ${under}=    Mock Transfer    receive    tb1q_inv1002    8000    notify=${True}
    Wait Until Keyword Succeeds    30s    500ms    Transfer Should Be Booked    ${under}[id]    confirmed
    ${run}=    Api    POST    /api/v1/reconciliation/runs    ${ADMIN}    ${ORG}
    ${stats}=    Set Variable    ${run.json()}[stats]
    Should Be Equal As Integers    ${stats}[missed_webhook]    1
    Should Be Equal As Integers    ${stats}[matched]    1
    Should Be Equal As Integers    ${stats}[amount_mismatch]    1
    Alert Should Be Open    recon_amount_mismatch

6 BitGo Outage Fills The DLQ; Scheduled Rules Raise Backlog And Token Alerts
    POST    ${MOCK_URL}/__mock/fail    json=${{{"Count": 100000}}}
    FOR    ${i}    IN RANGE    3
        ${tr}=    Mock Transfer    receive    tb1q_outage_${i}    ${{str(1000 + $i)}}
        ${body}=    Evaluate    json.dumps({"type": "transfer", "wallet": "wlt_btc_ops", "transfer": $tr["id"], "coin": "tbtc4"})
        POST    ${WEBHOOK_URL}    data=${body}    expected_status=202
    END
    Wait Until Keyword Succeeds    120s    2s    Queue Depth Should Be    ${DLQ_URL}    3
    POST    ${MOCK_URL}/__mock/fail    json=${{{"Count": 0}}}
    ${out}=    Run And Expect Success    ${JOB_ENV} ${BIN}/vaultctl reconcile
    Should Contain    ${out}    failed:0
    Alert Should Be Open    dlq_backlog
    Alert Should Be Open    bitgo_token_expiring
    Alert Should Be Open    webhook_dead_lettered

7 Daily Report And Audit Trail
    ${today}=    Evaluate    datetime.datetime.now(zoneinfo.ZoneInfo("Asia/Taipei")).strftime("%Y-%m-%d")    modules=datetime,zoneinfo
    ${rep}=    Api    GET    /api/v1/reports/daily?date\=${today}    ${ADMIN}    ${ORG}
    Should Contain    ${rep.text}    transfer,
    Should Contain    ${rep.text}    amount_mismatch
    Should Contain    ${rep.text}    150000000
    ${audit}=    Api    GET    /api/v1/audit    ${ADMIN}    ${ORG}
    Should Be True    ${audit.json()}[chainIntact]
    ${actions}=    Evaluate    {e["action"] for e in $audit.json()["entries"]}
    FOR    ${a}    IN    organization.created    bitgo_connection.updated    alert_settings.updated    transfer.booked    expected_payments.imported
        Should Contain    ${actions}    ${a}
    END

8 Dashboard Shows The Story And Alerts Can Be Resolved
    Open Browser    ${WEB_URL}/    headlesschrome
    Set Window Size    1280    2400
    Wait Until Element Is Visible    css:[data-testid="login-email"]    timeout=20s
    Input Text    css:[data-testid="login-email"]    admin@m5.example
    Click Button    css:[data-testid="login-submit"]
    Wait Until Element Is Visible    css:[data-testid="transfers-table"]    timeout=20s
    Wait Until Element Is Visible    css:[data-testid="alerts-list"]    timeout=20s
    FOR    ${kind}    IN    large_transfer    new_destination    wallet_policy_changed    dlq_backlog    bitgo_token_expiring
        Page Should Contain Element    css:[data-testid="alert-${kind}"]
    END
    Capture Page Screenshot    m5-dashboard.png
    Click Button    css:[data-testid="resolve-wallet_policy_changed"]
    Wait Until Page Does Not Contain Element    css:[data-testid="alert-wallet_policy_changed"]    timeout=20s
    ${alerts}=    Api    GET    /api/v1/alerts    ${ADMIN}    ${ORG}
    ${resolved}=    Evaluate    [a for a in $alerts.json()["alerts"] if a["kind"] == "wallet_policy_changed" and a["resolvedAt"]]
    Length Should Be    ${resolved}    1


*** Keywords ***
Start M5 Stack
    Start Full Stack    with_web=${True}
    ${t}=    Get Token    admin@m5.example
    Set Suite Variable    ${ADMIN}    ${t}
    ${org}    ${webhook}=    Create Org Via Api    ${t}    M5 Demo Treasury
    Set Suite Variable    ${ORG}    ${org}
    Set Suite Variable    ${WEBHOOK_URL}    ${EDGE_URL}/webhooks/bitgo/${webhook}
    Set Suite Variable    ${JOB_ENV}
    ...    VAULTOPS_ENV=local DATABASE_URL='${APP_DB_URL}' TOKEN_ENC_KEYS='${TOKEN_ENC_KEYS}' BITGO_BASE_URL='${MOCK_URL}' REPORTS_DIR='${RESULTS}/reports'

Mock Transfer
    [Arguments]    ${type}    ${address}    ${value}    ${notify}=${False}
    ${req}=    Create Dictionary    walletId=wlt_btc_ops    type=${type}    address=${address}    valueString=${value}    state=confirmed
    IF    ${notify}    Set To Dictionary    ${req}    notifyUrl=${WEBHOOK_URL}
    ${resp}=    POST    ${MOCK_URL}/__mock/transfers    json=${req}    expected_status=201
    RETURN    ${resp.json()}

Transfer Should Be Booked
    [Arguments]    ${provider_transfer_id}    ${state}
    ${r}=    Api    GET    /api/v1/transfers    ${ADMIN}    ${ORG}
    ${states}=    Evaluate    {t["providerTransferId"]: t["state"] for t in $r.json()["transfers"]}
    Should Be Equal    ${{$states.get($provider_transfer_id)}}    ${state}

Alert Should Be Open
    [Arguments]    ${kind}
    ${r}=    Api    GET    /api/v1/alerts    ${ADMIN}    ${ORG}
    ${open}=    Evaluate    [a for a in $r.json()["alerts"] if a["kind"] == $kind and not a["resolvedAt"]]
    Should Not Be Empty    ${open}    no open ${kind} alert

Queue Depth Should Be
    [Arguments]    ${queue}    ${expected}
    ${n}=    Queue Depth    ${queue}
    Should Be Equal As Integers    ${n}    ${expected}

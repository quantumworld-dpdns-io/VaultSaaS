*** Settings ***
Documentation       M4 — Reconciliation & Reporting.
...                 Acceptance (plan §6 M4): a missed webhook, an amount mismatch and an
...                 extra (unexpected) inbound are each classified correctly — plus
...                 matched, overdue and status-corrected cases — and the daily report
...                 is produced.
Resource            ../resources/common.resource
Library             SeleniumLibrary
Suite Setup         Start M4 Stack
Suite Teardown      Run Keywords    Close All Browsers    AND    Stop Local Stack
Test Tags           m4


*** Variables ***
${OPS}              ${EMPTY}
${ORG}              ${EMPTY}
${WEBHOOK_URL}      ${EMPTY}
${RUN_ID}           ${EMPTY}
&{TR}               &{EMPTY}


*** Test Cases ***
Go Tests Including Recon, Report And CSV Import Pass
    Run And Expect Success
    ...    VAULTOPS_TEST_APP_PASSWORD='${APP_DB_PASSWORD}' VAULTOPS_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54322/postgres?sslmode=disable' go test -count=1 ./...
    ...    cwd=${CORE_DIR}

Web Unit Tests Pass
    Run And Expect Success    npm test    cwd=${ROOT}/apps/web

Invalid CSV Rows Are Rejected And Nothing Is Imported
    ${r}=    Api Csv    /api/v1/expected-payments/import    ${OPS}    ${ORG}
    ...    reference,coin,address,amount\nBAD-1,tbtc4,addr,1.5\nBAD-2,tbtc4,addr,-1\n    expected=422
    Length Should Be    ${r.json()}[rows]    2
    ${n}=    Psql    SELECT count(*) FROM app.expected_payments
    Should Be Equal    ${n}    0

Every Scenario Is Classified Correctly
    [Documentation]    M4 acceptance criterion.
    ${csv}=    Catenate    SEPARATOR=\n
    ...    reference,coin,address,amount,expected_by
    ...    INV-A,tbtc4,addr_a,1000,
    ...    INV-B,tbtc4,addr_b,2000,
    ...    =SUM(1+1),tbtc4,addr_c,500,2020-01-01
    ...    INV-D,tbtc4,addr_d,4000,
    ${r}=    Api Csv    /api/v1/expected-payments/import    ${OPS}    ${ORG}    ${csv}\n
    Should Be Equal As Integers    ${r.json()}[imported]    4
    ${resp}=    Api    POST    /api/v1/reconciliation/runs    ${OPS}    ${ORG}
    Set Suite Variable    ${RUN_ID}    ${resp.json()}[runId]
    ${stats}=    Set Variable    ${resp.json()}[stats]
    Should Be Equal As Integers    ${stats}[missed_webhook]    3       # A, B, X never had a webhook
    Should Be Equal As Integers    ${stats}[status_mismatch]    1      # D: ledger pending, BitGo confirmed
    Should Be Equal As Integers    ${stats}[matched]    2              # A and D
    Should Be Equal As Integers    ${stats}[amount_mismatch]    1      # B: 1999 vs 2000
    Should Be Equal As Integers    ${stats}[missing_internal]    1     # X: unexpected inbound
    Should Be Equal As Integers    ${stats}[missing_bitgo]    1        # C: overdue
    ${items}=    Api    GET    /api/v1/reconciliation/runs/${RUN_ID}/items    ${OPS}    ${ORG}
    ${kinds}=    Evaluate    {(i["kind"], i["providerTransferId"] or i["reference"]) for i in $items.json()["items"]}
    Should Contain    ${kinds}    ${{("missed_webhook", $TR["a"])}}
    Should Contain    ${kinds}    ${{("amount_mismatch", $TR["b"])}}
    Should Contain    ${kinds}    ${{("missing_internal", $TR["x"])}}
    Should Contain    ${kinds}    ${{("status_mismatch", $TR["d"])}}
    Should Contain    ${kinds}    ${{("missing_bitgo", "=SUM(1+1)")}}
    Should Contain    ${kinds}    ${{("matched", $TR["a"])}}
    ${eps}=    Api    GET    /api/v1/expected-payments    ${OPS}    ${ORG}
    ${status}=    Evaluate    {p["reference"]: p["status"] for p in $eps.json()["expectedPayments"]}
    Should Be Equal    ${status}[INV-A]    matched
    Should Be Equal    ${status}[INV-B]    mismatched
    Should Be Equal    ${{$status["=SUM(1+1)"]}}    open
    Should Be Equal    ${status}[INV-D]    matched
    ${state}=    Psql    SELECT state FROM app.transfers WHERE provider_transfer_id = '${TR}[d]'
    Should Be Equal    ${state}    confirmed

Findings Raise Alerts
    ${alerts}=    Api    GET    /api/v1/alerts    ${OPS}    ${ORG}
    ${kinds}=    Evaluate    [a["kind"] for a in $alerts.json()["alerts"]]
    FOR    ${k}    IN    recon_amount_mismatch    recon_payment_overdue    recon_unexpected_inbound    webhook_gap
        Should Contain    ${kinds}    ${k}
    END

Scheduled Reconcile Repeats Nothing
    ${out}=    Run And Expect Success    ${JOB_ENV} ${BIN}/vaultctl reconcile
    Should Contain    ${out}    failed:0
    ${runs}=    Api    GET    /api/v1/reconciliation/runs    ${OPS}    ${ORG}
    ${latest}=    Set Variable    ${runs.json()}[runs][0]
    Should Be Equal    ${latest}[trigger]    schedule
    Should Be Equal    ${latest}[status]    succeeded
    ${repeat}=    Evaluate    {k: v for k, v in $latest["stats"].items() if k != "transfers_checked" and v}
    Should Be Empty    ${repeat}

Daily Report Contains Transfers And Escaped Findings
    ${today}=    Evaluate    datetime.datetime.now(zoneinfo.ZoneInfo("Asia/Taipei")).strftime("%Y-%m-%d")    modules=datetime,zoneinfo
    ${resp}=    Api    GET    /api/v1/reports/daily?date\=${today}    ${OPS}    ${ORG}
    Should Start With    ${resp.headers}[content-type]    text/csv
    Should Contain    ${resp.text}    record_type,timestamp,provider_transfer_id
    Should Contain    ${resp.text}    transfer,
    Should Contain    ${resp.text}    amount_mismatch
    Should Contain    ${resp.text}    '=SUM(1+1)
    Should Not Contain    ${resp.text}    ,=SUM
    Api    GET    /api/v1/reports/daily?date\=2999-01-01    ${OPS}    ${ORG}    expected=400
    ${out}=    Run And Expect Success    ${JOB_ENV} ${BIN}/vaultctl daily-report
    Should Contain    ${out}    published:

Viewers And Other Tenants Cannot Drive Reconciliation
    ${eve}=    Get Token    eve@m4.example
    ${other}    ${_}=    Create Org Via Api    ${eve}    Other Org
    Api Csv    /api/v1/expected-payments/import    ${eve}    ${ORG}    reference,coin,address,amount\nX,tbtc4,a,1\n    expected=403
    Api    POST    /api/v1/reconciliation/runs    ${eve}    ${ORG}    expected=403
    ${items}=    Api    GET    /api/v1/reconciliation/runs/${RUN_ID}/items    ${eve}    ${other}
    Should Be Empty    ${items.json()}[items]
    ${inv}=    Api    POST    /api/v1/org/invitations    ${OPS}    ${ORG}    body=${{{"email": "viewer@m4.example", "role": "viewer"}}}    expected=201
    ${viewer}=    Get Token    viewer@m4.example
    Api    POST    /api/v1/invitations/accept    ${viewer}    body=${{{"token": $inv.json()["token"]}}}
    Api    GET    /api/v1/reconciliation/runs    ${viewer}    ${ORG}
    Api    POST    /api/v1/reconciliation/runs    ${viewer}    ${ORG}    expected=403
    Api Csv    /api/v1/expected-payments/import    ${viewer}    ${ORG}    reference,coin,address,amount\nX,tbtc4,a,1\n    expected=403

Dashboard Imports, Reconciles And Shows Findings
    ${tr}=    Add Mock Transfer    addr_ui    321
    # Outside ~/Documents: macOS privacy controls can block the browser from reading files there.
    Create File    ${TEMPDIR}/vaultops-ui-import.csv    reference,coin,address,amount\nINV-UI,tbtc4,addr_ui,321\n
    Open Browser    ${WEB_URL}/    headlesschrome
    Wait Until Element Is Visible    css:[data-testid="login-email"]    timeout=20s
    Input Text    css:[data-testid="login-email"]    ops@m4.example
    Click Button    css:[data-testid="login-submit"]
    Wait Until Page Contains Element    css:[data-testid="import-csv"]    timeout=20s
    ${csv_path}=    Normalize Path    ${TEMPDIR}/vaultops-ui-import.csv
    Choose File    css:[data-testid="import-csv"]    ${csv_path}
    Wait Until Element Contains    css:[data-testid="import-result"]    Imported 1    timeout=20s

    Click Button    css:[data-testid="run-recon"]
    Wait Until Element Is Visible    css:[data-testid="finding-matched"]    timeout=30s
    Wait Until Element Contains    css:[data-testid="expected-INV-UI"] [data-testid="status"]    matched    timeout=20s
    Capture Page Screenshot    m4-reconciliation.png


*** Keywords ***
Start M4 Stack
    Start Full Stack    with_web=${True}
    ${t}=    Get Token    ops@m4.example
    Set Suite Variable    ${OPS}    ${t}
    ${org}    ${webhook}=    Create Org Via Api    ${t}    M4 Treasury
    Set Suite Variable    ${ORG}    ${org}
    Set Suite Variable    ${WEBHOOK_URL}    ${EDGE_URL}/webhooks/bitgo/${webhook}
    Connect BitGo    ${t}    ${org}
    Set Suite Variable    ${JOB_ENV}
    ...    VAULTOPS_ENV=local DATABASE_URL='${APP_DB_URL}' TOKEN_ENC_KEYS='${TOKEN_ENC_KEYS}' BITGO_BASE_URL='${MOCK_URL}' REPORTS_DIR='${RESULTS}/reports'
    # D: its first webhook arrives (booked pending), the confirmation webhook is lost.
    ${d}=    Add Mock Transfer    addr_d    4000    unconfirmed    notify=${True}
    Wait Until Keyword Succeeds    30s    500ms    Transfer State Should Be    ${d}[id]    pending
    POST    ${MOCK_URL}/__mock/transfers/state    json=${{{"WalletID": "wlt_btc_ops", "ID": $d["id"], "State": "confirmed"}}}
    # A, B, X: no webhook ever arrives.
    ${a}=    Add Mock Transfer    addr_a    1000
    ${b}=    Add Mock Transfer    addr_b    1999
    ${x}=    Add Mock Transfer    addr_x    777
    &{ids}=    Create Dictionary    a=${a}[id]    b=${b}[id]    x=${x}[id]    d=${d}[id]
    Set Suite Variable    &{TR}    &{ids}

Add Mock Transfer
    [Arguments]    ${address}    ${value}    ${state}=confirmed    ${notify}=${False}
    ${req}=    Create Dictionary    walletId=wlt_btc_ops    address=${address}    valueString=${value}    state=${state}
    IF    ${notify}    Set To Dictionary    ${req}    notifyUrl=${WEBHOOK_URL}
    ${resp}=    POST    ${MOCK_URL}/__mock/transfers    json=${req}    expected_status=201
    RETURN    ${resp.json()}

Transfer State Should Be
    [Arguments]    ${provider_transfer_id}    ${state}
    ${got}=    Psql    SELECT state FROM app.transfers WHERE provider_transfer_id = '${provider_transfer_id}'
    Should Be Equal    ${got}    ${state}


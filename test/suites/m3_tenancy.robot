*** Settings ***
Documentation       M3 — Tenancy, Auth, RBAC, Audit.
...                 Acceptance (plan §6 M3): every cross-tenant access (BOLA/IDOR) is
...                 refused, and modifying audit_logs is rejected by the trigger.
Resource            ../resources/common.resource
Library             SeleniumLibrary
Suite Setup         Start M3 Stack
Suite Teardown      Run Keywords    Close All Browsers    AND    Stop Local Stack
Test Tags           m3


*** Variables ***
${ALICE}            ${EMPTY}
${BOB}              ${EMPTY}
${ORG_A}            ${EMPTY}
${ORG_B}            ${EMPTY}
${RANDOM_ORG}       0f0f0f0f-0000-4000-8000-000000000000
${DEV_KEY}          4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318


*** Test Cases ***
Go Tests Including RLS, RBAC, Anchor And SIWE Pass
    Run And Expect Success
    ...    VAULTOPS_TEST_APP_PASSWORD='${APP_DB_PASSWORD}' VAULTOPS_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:54322/postgres?sslmode=disable' go test -count=1 ./...
    ...    cwd=${CORE_DIR}

Edge And Web Unit Tests Pass
    Run And Expect Success    npm test    cwd=${ROOT}/apps/edge
    Run And Expect Success    npm test    cwd=${ROOT}/apps/web

API Requires A Valid Supabase Token
    GET    ${EDGE_URL}/api/v1/me    expected_status=401
    ${h}=    Create Dictionary    Authorization=Bearer not.a.jwt
    GET    ${EDGE_URL}/api/v1/me    headers=${h}    expected_status=401
    ${resp}=    Api    GET    /api/v1/me    ${ALICE}
    Should Be Equal    ${resp.json()}[email]    alice@m3.example

Cross Tenant Access Is Refused On Every Org Endpoint
    [Documentation]    M3 acceptance criterion 1 (BOLA/IDOR). Same 403 for a foreign org and a non-existent one.
    FOR    ${target}    IN    ${ORG_B}    ${RANDOM_ORG}
        FOR    ${path}    IN    /api/v1/wallets    /api/v1/transfers    /api/v1/alerts    /api/v1/audit    /api/v1/org/members
            ${r}=    Api    GET    ${path}    ${ALICE}    ${target}    expected=403
            Should Be Equal    ${r.json()}[error]    forbidden
        END
        Api    POST    /api/v1/org/invitations    ${ALICE}    ${target}    body=${{{"email": "x@x.example", "role": "viewer"}}}    expected=403
        Api    PUT    /api/v1/org/bitgo-connection    ${ALICE}    ${target}    body=${{{"token": $BITGO_TOKEN}}}    expected=403
    END
    Api    GET    /api/v1/transfers    ${ALICE}    ${ORG_A}

Client Supplied Identity Headers Are Ignored
    ${bob_id}=    User Id Of    bob@m3.example
    Api    GET    /api/v1/transfers    ${ALICE}    ${ORG_B}    expected=403
    ...    X-Vaultops-User-Id=${bob_id}    X-Vaultops-Org-Id=${ORG_B}

Organizations Never Share A BitGo Connection
    Api    GET    /api/v1/wallets    ${ALICE}    ${ORG_A}
    ${r}=    Api    GET    /api/v1/wallets    ${BOB}    ${ORG_B}    expected=409
    Should Be Equal    ${r.json()}[error]    bitgo_not_connected
    ${sealed}=    Psql    SELECT encode(encrypted_token, 'escape') FROM app.bitgo_connections LIMIT 1
    Should Not Contain    ${sealed}    ${BITGO_TOKEN}

Viewer Can Read But Not Administer
    ${carol}=    Get Token    carol@m3.example
    ${inv}=    Api    POST    /api/v1/org/invitations    ${ALICE}    ${ORG_A}    body=${{{"email": "carol@m3.example", "role": "viewer"}}}    expected=201
    Api    POST    /api/v1/invitations/accept    ${carol}    body=${{{"token": $inv.json()["token"]}}}
    Api    GET    /api/v1/transfers    ${carol}    ${ORG_A}
    Api    GET    /api/v1/org/members    ${carol}    ${ORG_A}
    Api    GET    /api/v1/audit    ${carol}    ${ORG_A}    expected=403
    Api    POST    /api/v1/org/invitations    ${carol}    ${ORG_A}    body=${{{"email": "d@m3.example", "role": "viewer"}}}    expected=403
    Api    PUT    /api/v1/org/bitgo-connection    ${carol}    ${ORG_A}    body=${{{"token": $BITGO_TOKEN}}}    expected=403

Invitations Cannot Mint Owners Or Be Redeemed By Someone Else
    ${r}=    Api    POST    /api/v1/org/invitations    ${ALICE}    ${ORG_A}    body=${{{"email": "e@m3.example", "role": "owner"}}}    expected=403
    Should Be Equal    ${r.json()}[error]    cannot_grant_role
    ${inv}=    Api    POST    /api/v1/org/invitations    ${ALICE}    ${ORG_A}    body=${{{"email": "dave@m3.example", "role": "admin"}}}    expected=201
    Api    POST    /api/v1/invitations/accept    ${BOB}    body=${{{"token": $inv.json()["token"]}}}    expected=403
    ${dave}=    Get Token    dave@m3.example
    Api    POST    /api/v1/invitations/accept    ${dave}    body=${{{"token": $inv.json()["token"]}}}
    Api    POST    /api/v1/invitations/accept    ${dave}    body=${{{"token": $inv.json()["token"]}}}    expected=404

Audit Log Rejects Updates, Deletes And Truncation
    [Documentation]    M3 acceptance criterion 2.
    FOR    ${stmt}    IN    UPDATE app.audit_logs SET action = 'forged'    DELETE FROM app.audit_logs    TRUNCATE app.audit_logs
        ${out}=    Psql Expect Failure    ${stmt}
        Should Contain    ${out}    append-only
    END
    ${r}=    Api    GET    /api/v1/audit    ${ALICE}    ${ORG_A}
    Should Be True    ${r.json()}[chainIntact]
    ${actions}=    Evaluate    [e["action"] for e in $r.json()["entries"]]
    Should Contain    ${actions}    organization.created
    Should Contain    ${actions}    bitgo_connection.updated
    Should Contain    ${actions}    member.joined

Audit Anchor Detects A Recomputed History Rewrite
    Remove Directory    ${ANCHOR_DIR}    recursive=True
    Run And Expect Success    ${ANCHOR_ENV} ${BIN}/vaultctl audit-anchor
    # Owner-level attacker rewrites org A's history and recomputes its whole chain.
    ${sql}=    Catenate    SEPARATOR=\n
    ...    ALTER TABLE app.audit_logs DISABLE TRIGGER audit_no_update;
    ...    DO $$ DECLARE r app.audit_logs%ROWTYPE; prev bytea; BEGIN
    ...    UPDATE app.audit_logs SET data = '{"forged": true}' WHERE id = (SELECT min(id) FROM app.audit_logs WHERE organization_id = '${ORG_A}');
    ...    FOR r IN SELECT * FROM app.audit_logs WHERE organization_id = '${ORG_A}' ORDER BY id LOOP
    ...    UPDATE app.audit_logs SET prev_hash = prev, hash = app.audit_row_digest(r, prev) WHERE id = r.id;
    ...    prev := app.audit_row_digest(r, prev); END LOOP; END $$;
    ...    ALTER TABLE app.audit_logs ENABLE TRIGGER audit_no_update;
    Psql File    ${sql}
    ${r}=    Api    GET    /api/v1/audit    ${ALICE}    ${ORG_A}
    Should Be True    ${r.json()}[chainIntact]    msg=precondition: the recomputed chain verifies internally
    ${res}=    Run Process    bash    -c    ${ANCHOR_ENV} ${BIN}/vaultctl audit-anchor    cwd=${ROOT}    stderr=STDOUT
    Should Not Be Equal As Integers    ${res.rc}    0
    Should Contain    ${res.stdout}    audit history was rewritten
    ${alerts}=    Api    GET    /api/v1/alerts    ${ALICE}    ${ORG_A}
    ${kinds}=    Evaluate    [a["kind"] for a in $alerts.json()["alerts"]]
    Should Contain    ${kinds}    audit_anchor_mismatch

D4 Wallets Are Linked Identities, Never A Way In
    ${anon}=    Create Dictionary    Content-Type=application/json
    POST    ${EDGE_URL}/api/v1/me/wallets    headers=${anon}    data={}    expected_status=401
    Api    POST    /api/v1/siwe/login    ${ALICE}    body=${{{}}}    expected=404
    ${n}=    Api    POST    /api/v1/me/wallets/nonce    ${ALICE}
    ${signed}=    Dev Sign    ${n.json()}[nonce]
    ${r}=    Api    POST    /api/v1/me/wallets    ${ALICE}    body=${{{"message": $signed["message"], "signature": $signed["signature"]}}}    expected=201
    Should Be Equal    ${r.json()}[address]    ${{$signed["address"].lower()}}
    Api    POST    /api/v1/me/wallets    ${ALICE}    body=${{{"message": $signed["message"], "signature": $signed["signature"]}}}    expected=401
    ${n2}=    Api    POST    /api/v1/me/wallets/nonce    ${BOB}
    ${signed2}=    Dev Sign    ${n2.json()}[nonce]
    Api    POST    /api/v1/me/wallets    ${BOB}    body=${{{"message": $signed2["message"], "signature": $signed2["signature"]}}}    expected=409
    ${me}=    Api    GET    /api/v1/me    ${ALICE}
    Length Should Be    ${me.json()}[linkedWallets]    1

Dashboard Shows Org Switcher, Members And Linked Wallets
    Open Browser    ${WEB_URL}/    headlesschrome
    Wait Until Element Is Visible    css:[data-testid="login-email"]    timeout=20s
    Input Text    css:[data-testid="login-email"]    alice@m3.example
    Click Button    css:[data-testid="login-submit"]
    Wait Until Element Is Visible    css:[data-testid="org-switcher"]    timeout=20s
    Wait Until Element Is Visible    css:[data-testid="wallets-table"]    timeout=20s
    Wait Until Element Contains    css:[data-testid="members-list"]    carol@m3.example    timeout=20s
    Element Should Contain    css:[data-testid="linked-wallets"]    0x
    Input Text    css:[data-testid="invite-email"]    erin@m3.example
    Click Button    css:[data-testid="invite-submit"]
    Wait Until Element Contains    css:[data-testid="invite-link"]    ?invite=    timeout=20s
    Capture Page Screenshot    m3-dashboard.png


*** Keywords ***
Start M3 Stack
    Start Full Stack    with_web=${True}
    ${a}=    Get Token    alice@m3.example
    ${b}=    Get Token    bob@m3.example
    Set Suite Variable    ${ALICE}    ${a}
    Set Suite Variable    ${BOB}    ${b}
    ${oa}    ${_}=    Create Org Via Api    ${a}    Alpha Treasury
    ${ob}    ${_}=    Create Org Via Api    ${b}    Beta Treasury
    Set Suite Variable    ${ORG_A}    ${oa}
    Set Suite Variable    ${ORG_B}    ${ob}
    Connect BitGo    ${a}    ${oa}
    Set Suite Variable    ${ANCHOR_ENV}
    ...    VAULTOPS_ENV=local DATABASE_URL='${APP_DB_URL}' ANCHOR_DIR='${ANCHOR_DIR}'

Psql Expect Failure
    [Arguments]    ${sql}
    ${r}=    Run Process    ${COMPOSE} exec -T postgres psql -U postgres -d ${ROBOT_DB} -v ON_ERROR_STOP\=1 -tAc "${sql}"
    ...    shell=True    stderr=STDOUT
    Should Not Be Equal As Integers    ${r.rc}    0    msg=expected failure but succeeded: ${sql}
    RETURN    ${r.stdout}

Psql File
    [Arguments]    ${sql}
    Create File    ${RESULTS}/tamper.sql    ${sql}
    Run And Expect Success    ${COMPOSE} exec -T postgres psql -U postgres -d ${ROBOT_DB} -v ON_ERROR_STOP=1 -q < ${RESULTS}/tamper.sql

Dev Sign
    [Arguments]    ${nonce}
    ${out}=    Run And Expect Success    ${BIN}/devsign -domain ${SIWE_DOMAIN} -nonce ${nonce} -key ${DEV_KEY}
    ${json}=    Evaluate    json.loads($out)
    RETURN    ${json}

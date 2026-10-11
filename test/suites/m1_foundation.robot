*** Settings ***
Documentation       M1 — Foundation.
...                 Acceptance (plan §6 M1): the dashboard prototype lists BitGo testnet
...                 wallets and balances. BitGo is replaced by the mock until a testnet
...                 account exists (D9); nothing is deployed.
Resource            ../resources/common.resource
Library             SeleniumLibrary
Suite Setup         Start M1 Stack
Suite Teardown      Run Keywords    Close All Browsers    AND    Stop Local Stack
Test Tags           m1


*** Test Cases ***
Go Core Unit Tests Pass
    Run And Expect Success    go vet ./... && go test ./...    cwd=${CORE_DIR}

Edge Worker Unit Tests Pass
    Run And Expect Success    npm test    cwd=${ROOT}/apps/edge

Web Unit Tests Pass
    Run And Expect Success    npm test    cwd=${ROOT}/apps/web

Lambda Artifacts Build For Linux Arm64
    Run And Expect Success    make build-core
    ${out}=    Run And Expect Success    file ${CORE_DIR}/bin/api/bootstrap
    Should Match Regexp    ${out}    ELF 64-bit.*(ARM aarch64|arm64)
    File Should Exist    ${CORE_DIR}/bin/api.zip

Terraform Roots Are Formatted And Valid
    Run And Expect Success    make tf-validate

Edge Health Check Responds
    ${resp}=    GET    ${EDGE_URL}/healthz    expected_status=200
    Should Be Equal    ${resp.json()}[service]    edge

Core Health Check Is Reachable End To End Through The Edge
    ${resp}=    GET    ${EDGE_URL}/api/healthz    expected_status=200
    Should Be Equal    ${resp.json()}[service]    core
    Should Be Equal    ${resp.headers}[cache-control]    no-store

Wallets Are Listed With String Balances Through The Edge
    ${token}=    Get Token    m1-api@example.com
    ${org}    ${_}=    Create Org Via Api    ${token}    M1 API Org
    Connect BitGo    ${token}    ${org}
    ${resp}=    Api    GET    /api/v1/wallets    ${token}    ${org}
    ${wallets}=    Set Variable    ${resp.json()}[wallets]
    Length Should Be    ${wallets}    2
    Should Be Equal    ${wallets}[0][providerWalletId]    wlt_btc_ops
    Should Be Equal    ${wallets}[0][balance]    150000000
    Should Be Equal    ${wallets}[1][balance]    2500000000000000000
    Should Be String    ${wallets}[1][balance]

Core Uses The BitGo Token And Only Read Endpoints
    ${resp}=    GET    ${MOCK_URL}/__mock/requests    expected_status=200
    FOR    ${req}    IN    @{resp.json()}[requests]
        Should Start With    ${req}    GET${SPACE}
    END

Upstream BitGo Errors Are Not Leaked
    ${token}=    Get Token    m1-outage@example.com
    ${org}    ${_}=    Create Org Via Api    ${token}    M1 Outage Org
    Connect BitGo    ${token}    ${org}
    POST    ${MOCK_URL}/__mock/fail    json=${{{"Count": 10}}}
    ${resp}=    Api    GET    /api/v1/wallets    ${token}    ${org}    expected=502
    Should Be Equal    ${resp.text.strip()}    {"error":"bitgo_unavailable"}
    [Teardown]    POST    ${MOCK_URL}/__mock/fail    json=${{{"Count": 0}}}

Dashboard Lists Testnet Wallets And Balances
    [Documentation]    The M1 acceptance criterion, through the UI: sign in, create an org,
    ...    connect BitGo, see wallets and balances.
    Open Browser    ${WEB_URL}/    headlesschrome
    Wait Until Element Is Visible    css:[data-testid="login-email"]    timeout=20s
    Input Text    css:[data-testid="login-email"]    m1-ui@example.com
    Click Button    css:[data-testid="login-submit"]
    Wait Until Element Is Visible    css:[data-testid="org-name"]    timeout=20s
    Input Text    css:[data-testid="org-name"]    M1 UI Org
    Click Button    css:[data-testid="org-create"]
    Wait Until Element Is Visible    css:[data-testid="webhook-url"]    timeout=20s
    Wait Until Element Is Visible    css:[data-testid="bitgo-token"]    timeout=20s
    Input Text    css:[data-testid="bitgo-token"]    ${BITGO_TOKEN}
    Click Button    css:[data-testid="connect-submit"]
    Wait Until Element Is Visible    css:[data-testid="wallets-table"]    timeout=20s
    Element Should Contain    css:[data-testid="wallet-wlt_btc_ops"]    BTC Operations
    Element Should Contain    css:[data-testid="wallet-wlt_btc_ops"] [data-testid="balance"]    1.5
    Element Should Contain    css:[data-testid="wallet-wlt_eth_treasury"]    ETH Treasury
    Element Should Contain    css:[data-testid="wallet-wlt_eth_treasury"] [data-testid="balance"]    2.5
    Capture Page Screenshot    m1-dashboard.png


*** Keywords ***
Start M1 Stack
    Start Full Stack    with_web=${True}

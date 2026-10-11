*** Settings ***
Documentation       Shared keywords: building and starting the local stack
...                 (mock BitGo -> Go core -> wrangler edge -> Vite dashboard).
Library             Process
Library             OperatingSystem
Library             String
Library             Collections
Library             RequestsLibrary


*** Variables ***
${ROOT}                 ${CURDIR}/../..
${CORE_DIR}             ${ROOT}/services/core
${RESULTS}              ${CURDIR}/../results
${BIN}                  ${RESULTS}/bin
${MOCK_PORT}            18090
${CORE_PORT}            18080
${EDGE_PORT}            18787
${WEB_PORT}             15173
${BITGO_TOKEN}          robot-bitgo-token
${MOCK_URL}             http://127.0.0.1:${MOCK_PORT}
${CORE_URL}             http://127.0.0.1:${CORE_PORT}
${EDGE_URL}             http://127.0.0.1:${EDGE_PORT}
${WEB_URL}              http://127.0.0.1:${WEB_PORT}


*** Keywords ***
Run And Expect Success
    [Documentation]    Runs a command in the repo and fails with its output if it exits non-zero.
    [Arguments]    ${command}    ${cwd}=${ROOT}    ${timeout}=600s
    ${result}=    Run Process    bash    -c    ${command}    cwd=${cwd}    timeout=${timeout}
    ...    stderr=STDOUT    on_timeout=kill
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=`${command}` failed:\n${result.stdout}
    RETURN    ${result.stdout}

Build Core Binaries
    Create Directory    ${BIN}
    Run And Expect Success    go build -o ${BIN}/core-local ./cmd/local && go build -o ${BIN}/mockbitgo ./cmd/mockbitgo
    ...    cwd=${CORE_DIR}

Wait For Http
    [Arguments]    ${url}    ${timeout}=90s
    Wait Until Keyword Succeeds    ${timeout}    1s    Url Should Respond    ${url}

Url Should Respond
    [Arguments]    ${url}
    ${resp}=    GET    ${url}    expected_status=anything    timeout=2
    Should Be True    ${resp.status_code} < 500    ${url} answered ${resp.status_code}

Start Mock BitGo
    Start Process    ${BIN}/mockbitgo    alias=mockbitgo    cwd=${ROOT}
    ...    env:MOCK_BITGO_ADDR=127.0.0.1:${MOCK_PORT}    env:MOCK_BITGO_TOKEN=${BITGO_TOKEN}
    ...    stdout=${RESULTS}/mockbitgo.log    stderr=STDOUT
    Wait For Http    ${MOCK_URL}/__mock/requests

Start Core
    [Arguments]    ${port}=${CORE_PORT}    ${token}=${BITGO_TOKEN}    ${alias}=core    &{extra_env}
    Start Process    ${BIN}/core-local    alias=${alias}    cwd=${ROOT}
    ...    env:CORE_ADDR=127.0.0.1:${port}    env:BITGO_BASE_URL=${MOCK_URL}    env:BITGO_TOKEN=${token}
    ...    env:VAULTOPS_ENV=local    &{extra_env}
    ...    stdout=${RESULTS}/${alias}.log    stderr=STDOUT
    Wait For Http    http://127.0.0.1:${port}/healthz

Start Edge
    Start Process    npx    wrangler    dev    --local    --ip    127.0.0.1    --port    ${EDGE_PORT}
    ...    --var    CORE_URL:${CORE_URL}    --var    ALLOWED_ORIGINS:${WEB_URL}
    ...    alias=edge    cwd=${ROOT}/apps/edge
    ...    env:WRANGLER_SEND_METRICS=false    env:CI=1
    ...    stdout=${RESULTS}/edge.log    stderr=STDOUT
    Wait For Http    ${EDGE_URL}/healthz    timeout=120s

Start Web
    Run And Expect Success    npx tsc --noEmit && VITE_API_BASE=${EDGE_URL} npx vite build --outDir ${RESULTS}/web-dist --emptyOutDir
    ...    cwd=${ROOT}/apps/web
    Start Process    npx    vite    preview    --outDir    ${RESULTS}/web-dist    --host    127.0.0.1
    ...    --port    ${WEB_PORT}    --strictPort
    ...    alias=web    cwd=${ROOT}/apps/web
    ...    stdout=${RESULTS}/web.log    stderr=STDOUT
    Wait For Http    ${WEB_URL}/

Reset Mock BitGo
    POST    ${MOCK_URL}/__mock/reset    expected_status=200

Stop Local Stack
    Terminate All Processes    kill=True

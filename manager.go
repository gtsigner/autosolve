package autosolve

import (
	"encoding/json"
	"errors"
	"github.com/streadway/amqp"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	ClientId string
}
type AYCDManager struct {
	locker            sync.Locker
	account           Account
	connection        *amqp.Connection
	directChannel     *amqp.Channel
	fanoutChannel     *amqp.Channel
	connectionPending bool
	explicitShutdown  bool

	directExchangeName          string
	fanoutExchangeName          string
	responseQueueName           string
	responseTokenRouteKey       string
	responseTokenCancelRouteKey string
	requestTokenRouteKey        string
	requestTokenCancelRouteKey  string

	clientId    string
	apiKey      string
	accessToken string

	//runtime
	lastConnectAttempt  int64
	connectAttemptDelay int64
	reconnectAttempt    int
	reconnectTimeouts   []time.Duration

	//
	listener IListener
}
type IListener interface {
	StatusListener(status Status)
	ErrorListener(err error)
	CaptchaTokenCancelResponseListener(response CaptchaTokenCancelResponse)
	CaptchaTokenResponseListener(response CaptchaTokenResponse)
}

func NewManager(options Options) *AYCDManager {
	m := &AYCDManager{}
	m.clientId = options.ClientId
	m.reconnectTimeouts = []time.Duration{2, 3, 5, 8, 13, 21, 34}
	return m
}

func (m *AYCDManager) Set(accessToken, apiKey string) {
	m.apiKey = apiKey
	m.accessToken = accessToken
}
func (m *AYCDManager) Load(listener IListener) error {
	m.locker.Lock()
	defer m.locker.Unlock()
	m.listener = listener
	return nil
}

func (m *AYCDManager) Connect(accessToken, apiKey string) (ConnectResult, error) {
	m.Set(accessToken, apiKey)
	if m.connectionPending {
		return ConnectionPending, nil
	}
	_ = m.Close()
	m.explicitShutdown = false
	if !isNotEmpty(m.apiKey) {
		return InvalidApiKey, nil
	}
	if isNotEmpty(m.accessToken) {
		tokenParts := strings.Split(m.accessToken, "-")
		if len(tokenParts) > 0 {
			aId, err := strconv.Atoi(tokenParts[0])
			if !checkError(err) {
				m.account = Account{
					id:           aId,
					rId:          strconv.Itoa(aId),
					accessToken:  m.accessToken,
					rAccessToken: replaceAllDashes(m.accessToken),
					apiKey:       m.apiKey,
					rApiKey:      replaceAllDashes(m.apiKey),
				}
				return m.connect(false)
			}
		}
	}
	return InvalidAccessToken, nil
}
func (m *AYCDManager) connect(reconnect bool) (ConnectResult, error) {
	if m.connectionPending {
		return ConnectionPending, nil
	}
	m.connectionPending = true
	if !reconnect {
		m.updateStatus(Connecting)
	}
	currentTime := getCurrentUnixTime()
	elapsedTime := currentTime - m.lastConnectAttempt
	if elapsedTime < m.connectAttemptDelay {
		sleepTime := time.Duration(m.connectAttemptDelay - elapsedTime)
		duration := sleepTime * time.Second
		time.Sleep(duration)
	}
	m.lastConnectAttempt = getCurrentUnixTime()
	var result, err = m.verifyCredentials()
	if result == Success {
		m.createKeys()
		err = m.startConnection()
		if !checkError(err) {
			err = m.registerConsumers()
			if !checkError(err) {
				m.updateStatus(Connected)
				m.reconnectAttempt = 0
				go m.registerNotifyClose()
				err = nil
			}
		}
		if err != nil {
			if m.connection != nil {
				defer m.connection.Close()
			}
			result = ConnectionError
		}
	}
	if err != nil && !reconnect {
		m.updateStatus(Disconnected)
	}
	m.connectionPending = false
	return result, err
}
func (m *AYCDManager) updateStatus(status Status) {
	m.listener.StatusListener(status)
}

func (m *AYCDManager) SendTokenRequest(request CaptchaTokenRequest) error {
	if m.IsClosed() {
		return errors.New("the connection is not available")
	}
	request.ApiKey = m.account.apiKey
	request.CreatedAt = getCurrentUnixTime()
	var jsonData []byte
	jsonData, err := json.Marshal(request)
	if !checkError(err) {
		err = m.directChannel.Publish(
			m.directExchangeName,
			m.requestTokenRouteKey,
			false,
			false,
			amqp.Publishing{
				ContentType: "text/plain",
				Body:        jsonData,
			})
	}
	return err
}
func (m *AYCDManager) SendTokenCancelRequest(request CaptchaTokenCancelRequest) error {
	if m.IsClosed() {
		return errors.New("the connection is not available")
	}
	request.ApiKey = m.account.apiKey
	request.CreatedAt = getCurrentUnixTime()
	var jsonData []byte
	jsonData, err := json.Marshal(request)
	if !checkError(err) {
		err = m.fanoutChannel.Publish(
			m.fanoutExchangeName,
			m.requestTokenCancelRouteKey,
			false,
			false,
			amqp.Publishing{
				ContentType: "text/plain",
				Body:        jsonData,
			})
	}
	return err
}

func (m *AYCDManager) IsConnected() bool {
	return !m.IsClosed()
}

func (m *AYCDManager) IsClosed() bool {
	return m.connection == nil || m.connection.IsClosed()
}
func (m *AYCDManager) Close() error {
	var err error = nil
	m.explicitShutdown = true
	if !m.IsClosed() {
		err = m.connection.Close()
	}
	return err
}
func (m *AYCDManager) registerNotifyClose() {
	closeCh := make(chan *amqp.Error)
	m.connection.NotifyClose(closeCh)
	connErr := <-closeCh
	if m.explicitShutdown {
		m.updateStatus(Disconnected)
	} else if connErr != nil {
		m.listener.ErrorListener(connErr)
		go m.reconnect()
	}
}
func (m *AYCDManager) reconnect() {
	for m.IsClosed() && !m.explicitShutdown {
		m.updateStatus(Reconnecting)
		timeout := m.getNextReconnectTimeout()
		duration := timeout * time.Second
		time.Sleep(duration)
		_, err := m.connect(true)
		if checkError(err) {
			m.listener.ErrorListener(err)
		}
	}
}
func (m *AYCDManager) createKeys() {
	m.directExchangeName = m.createKeyWithAccountId(directExchangePrefix)
	m.fanoutExchangeName = m.createKeyWithAccountId(fanoutExchangePrefix)

	m.responseQueueName = m.createKeyWithAccountIdAndApiKey(responseQueuePrefix)
	m.responseTokenRouteKey = m.createKeyWithAccountIdAndApiKey(responseTokenRoutePrefix)
	m.responseTokenCancelRouteKey = m.createKeyWithAccountIdAndApiKey(responseTokenCancelRoutePrefix)

	m.requestTokenRouteKey = m.createKeyWithAccessToken(requestTokenRoutePrefix)
	m.requestTokenCancelRouteKey = m.createKeyWithAccessToken(requestTokenCancelRoutePrefix)
}
func (m *AYCDManager) getNextReconnectTimeout() time.Duration {
	var index int
	if m.reconnectAttempt >= len(m.reconnectTimeouts) {
		index = len(m.reconnectTimeouts) - 1
	} else {
		index = m.reconnectAttempt
		m.reconnectAttempt += 1
	}
	return m.reconnectTimeouts[index]
}
func (m *AYCDManager) createKeyWithAccountIdAndApiKey(prefix string) string {
	return prefix + "." + m.account.rId + "." + m.account.rApiKey
}
func (m *AYCDManager) createKeyWithAccessToken(prefix string) string {
	return prefix + "." + m.account.rAccessToken
}

func (m *AYCDManager) createKeyWithAccountId(prefix string) string {
	return prefix + "." + m.account.rId
}

func (m *AYCDManager) startConnection() error {
	conn, err := amqp.Dial("amqp://" + strconv.Itoa(m.account.id) + ":" + m.account.accessToken + "@" + hostname + ":5672/" + vHost + "?heartbeat=10")
	if checkError(err) {
		return err
	}
	m.connection = conn
	directCh, err := conn.Channel()
	if !checkError(err) {
		fanoutCh, err := conn.Channel()
		if !checkError(err) {
			m.directChannel = directCh
			m.fanoutChannel = fanoutCh
			return nil
		}
	}
	return err
}

func (m *AYCDManager) registerConsumers() error {
	err := m.bindQueues()
	if !checkError(err) {
		messages, err := m.directChannel.Consume(
			m.responseQueueName,
			"",
			autoAckQueue,
			exclusiveQueue,
			false,
			false,
			nil,
		)
		if !checkError(err) {
			go func() {
				for delivery := range messages {
					switch delivery.RoutingKey {
					case m.responseTokenRouteKey:
						m.processTokenMessage(&delivery)
					case m.responseTokenCancelRouteKey:
						m.processTokenCancelMessage(&delivery)
					}
				}
			}()
		}
	}
	return err
}

func (m *AYCDManager) processTokenMessage(message *amqp.Delivery) {
	var tokenResponse CaptchaTokenResponse
	var err = json.Unmarshal(message.Body, &tokenResponse)
	if !checkError(err) {
		m.listener.CaptchaTokenResponseListener(tokenResponse)
	} else {
		m.listener.ErrorListener(err)
	}
}

func (m *AYCDManager) processTokenCancelMessage(message *amqp.Delivery) {
	var tokenCancelResponse CaptchaTokenCancelResponse
	var err = json.Unmarshal(message.Body, &tokenCancelResponse)
	if !checkError(err) {
		m.listener.CaptchaTokenCancelResponseListener(tokenCancelResponse)
	} else {
		m.listener.ErrorListener(err)
	}
}

func (m *AYCDManager) bindQueues() error {
	var err error
	err = m.directChannel.QueueBind(
		m.responseQueueName,
		m.responseTokenRouteKey,
		m.directExchangeName,
		false,
		nil,
	)
	if !checkError(err) {
		err = m.directChannel.QueueBind(
			m.responseQueueName,
			m.responseTokenCancelRouteKey,
			m.directExchangeName,
			false,
			nil,
		)
	}
	return err
}

func (m *AYCDManager) verifyCredentials() (ConnectResult, error) {
	url := "https://dash.autosolve.aycd.io/rest/" + m.account.accessToken + "/verify/" + m.account.apiKey + "?clientId=" + m.clientId
	response, err := http.Get(url)
	if checkError(err) {
		return VerificationError, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return Success, nil
	case http.StatusBadRequest:
		return InvalidClientId, nil
	case http.StatusUnauthorized:
		return InvalidCredentials, nil
	case http.StatusTooManyRequests:
		return TooManyRequests, nil
	default:
		return UnknownError, nil
	}
}

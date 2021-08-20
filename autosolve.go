package autosolve

import (
	"time"

	"strings"
)

const (
	hostname             = "amqp.autosolve.aycd.io"
	vHost                = "oneclick"
	directExchangePrefix = "exchanges.direct"
	fanoutExchangePrefix = "exchanges.fanout"

	responseQueuePrefix = "queues.response.direct"

	requestTokenRoutePrefix        = "routes.request.token"
	requestTokenCancelRoutePrefix  = "routes.request.token.cancel"
	responseTokenRoutePrefix       = "routes.response.token"
	responseTokenCancelRoutePrefix = "routes.response.token.cancel"

	autoAckQueue   = true
	exclusiveQueue = false
)

func replaceAllDashes(key string) string {
	return strings.Replace(key, "-", "", -1)
}

func checkError(err error) bool {
	return err != nil
}

func isNotEmpty(str string) bool {
	return len(str) > 0
}

func getCurrentUnixTime() int64 {
	return time.Now().Unix()
}

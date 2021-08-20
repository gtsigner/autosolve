package autosolve

import (
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
)

type I struct {
}

func (i I) StatusListener(status Status) {
	log.Printf("StatusListener: %v \n", status)
}

func (i I) ErrorListener(err error) {
	log.Printf("StatusListener: %v \n", err)
}

func (i I) CaptchaTokenCancelResponseListener(response CaptchaTokenCancelResponse) {
	log.Printf("Cancel: %v \n", response)
}

func (i I) CaptchaTokenResponseListener(response CaptchaTokenResponse) {
	log.Printf("Resp: %v \n", response)
}

func TestNewManager(t *testing.T) {
	clientId := os.Getenv("CLIENT_ID")
	accessToken := os.Getenv("ACCESS_TOKEN")
	apiKey := os.Getenv("API_KEY")
	if clientId == "" || accessToken == "" || apiKey == "" {
		panic("please input your params to env")
	}
	manger := NewClient(Options{
		ClientId: clientId,
	})
	callback := I{}
	err := manger.Load(callback)
	if err != nil {
		panic(err)
	}
	res, err := manger.Connect(accessToken, apiKey)
	switch res {
	case Success:
		print("Successful Connection")
		var message = CaptchaTokenRequest{
			TaskId:  "1",
			Url:     "https://recaptcha.io/version/1",
			SiteKey: "6Ld_LMAUAAAAAOIqLSy5XY9-DUKLkAgiDpqtTJ9b",
		}
		err := manger.SendTokenRequest(message)
		log.Printf("SendTokenRequest: %v \n", err)
		message.TaskId = "7"
		err = manger.SendTokenRequest(message)
		log.Printf("SendTokenRequest: %v \n", err)
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
	case InvalidClientId:
		print("Invalid Client Key")
	case InvalidAccessToken:
		print("Invalid access token")
	case InvalidApiKey:
		print("Invalid Api Key")
	case InvalidCredentials:
		print("Invalid Credentials")
	}
}

func TestNa(t *testing.T) {
	fmt.Println("tt", t)
}

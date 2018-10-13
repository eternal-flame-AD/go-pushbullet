// Package pushbullet provides simple access to the v2 API of http://pushbullet.com.
/*

Example client:
	pb := pushbullet.New("YOUR_API_KEY")
	devices, err := pb.Devices()
	...
	err = pb.PushNote(devices[0].Iden, "Hello!", "Hi from go-pushbullet!")

The API is document at https://docs.pushbullet.com/http/ .  At the moment, it only supports querying devices and sending notifications.

*/
package pushbullet

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// ErrDeviceNotFound is raised when device nickname is not found on pusbullet server
var ErrDeviceNotFound = errors.New("Device not found")

// EndpointURL sets the default URL for the Pushbullet API
var EndpointURL = "https://api.pushbullet.com/v2"

// Endpoint allows manipulation of pushbullet API endpoint for testing
type Endpoint struct {
	URL string
}

// A Client connects to PushBullet with an API Key.
type Client struct {
	Key    string
	Client *http.Client
	Endpoint
}

type Listener struct {
	Ephemeral <-chan *EphemeralPush
	Push      <-chan *Push
	Device    <-chan *Device
	Error     <-chan error
	Close     chan<- struct{}
	Updated   time.Time
}

func (c *Client) Listen() *Listener {
	u := url.URL{
		Scheme: "wss",
		Host:   "stream.pushbullet.com",
		Path:   "/websocket/" + c.Key,
	}
	cache := 10
	ephemeral := make(chan *EphemeralPush, cache)
	pushc := make(chan *Push, cache)
	devicec := make(chan *Device, cache)
	errorc := make(chan error, cache)
	nclose := make(chan struct{}, 1)
	listener := &Listener{
		ephemeral, pushc, devicec, errorc, nclose, time.Now(),
	}
	go func() {
		connect := func() *websocket.Conn {
			conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				errorc <- err
			}
			return conn
		}
		update := func(new time.Time) {
			listener.Updated = new
		}
		conn := connect()
		for {
			conn.SetReadDeadline(time.Now().Add(time.Minute))
			_, r, err := conn.NextReader()
			if err != nil {
				errorc <- err
				conn = connect()
				continue
			}
			msg, err := ioutil.ReadAll(r)
			if err != nil {
				errorc <- err
				conn = connect()
				continue
			}
			data := struct {
				Type    string         `json:"type"`
				Subtype string         `json:"subtype"`
				Push    *EphemeralPush `json:"push"`
			}{}
			json.Unmarshal(msg, &data)
			switch data.Type {
			case "nop":
			case "tickle":
				switch data.Subtype {
				case "push":
					pushes, err := c.Pushes(listener.Updated, false, "", 0)
					if err != nil {
						errorc <- err
					}
					if len(pushes) == 0 {
						continue
					}
					newest := pushes[0].Modified
					for _, push := range pushes {
						if int64(push.Modified) > listener.Updated.Unix() {
							pushc <- push
						}
						if push.Modified > newest {
							newest = push.Modified
						}
					}
					update(time.Unix(int64(newest), 0))
				case "device":
					devices, err := c.Devices()
					if err != nil {
						errorc <- err
					}
					if len(devices) == 0 {
						continue
					}
					newest := devices[0].Modified
					for _, device := range devices {
						if int64(device.Modified) > listener.Updated.Unix() {
							devicec <- device
						}
						if device.Modified > newest {
							newest = device.Modified
						}
					}
					update(time.Unix(int64(newest), 0))
				}
			case "push":
				ephemeral <- data.Push
			}
			select {
			case <-nclose:
				close(pushc)
				close(ephemeral)
				close(errorc)
				close(nclose)
				close(devicec)
				break
			default:
			}
		}
	}()
	return listener
}

// New creates a new client with your personal API key.
func New(apikey string) *Client {
	endpoint := Endpoint{URL: EndpointURL}
	return &Client{apikey, http.DefaultClient, endpoint}
}

// NewWithClient creates a new client with your personal API key and the given http Client
func NewWithClient(apikey string, client *http.Client) *Client {
	endpoint := Endpoint{URL: EndpointURL}
	return &Client{apikey, client, endpoint}
}

// A Device is a PushBullet device
type Device struct {
	Iden              string  `json:"iden"`
	Active            bool    `json:"active"`
	Created           float64 `json:"created"`
	Modified          float64 `json:"modified"`
	Icon              string  `json:"icon"`
	Nickname          string  `json:"nickname"`
	GeneratedNickname bool    `json:"generated_nickname"`
	Manufacturer      string  `json:"manufacturer"`
	Model             string  `json:"model"`
	AppVersion        int     `json:"app_version"`
	Fingerprint       string  `json:"fingerprint"`
	KeyFingerprint    string  `json:"key_fingerprint"`
	PushToken         string  `json:"push_token"`
	HasSms            bool    `json:"has_sms"`
	Client            *Client `json:"-"`
}

// ErrResponse is an error returned by the PushBullet API
type ErrResponse struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Cat     string `json:"cat"`
}

func (e *ErrResponse) Error() string {
	return e.Message
}

type errorResponse struct {
	ErrResponse `json:"error"`
}

type deviceResponse struct {
	Devices       []*Device
	SharedDevices []*Device `json:"shared_devices"`
}

type subscriptionResponse struct {
	Subscriptions []*Subscription
}

func (c *Client) buildRequest(object string, data interface{}) *http.Request {
	r, err := http.NewRequest("GET", c.Endpoint.URL+object, nil)
	if err != nil {
		panic(err)
	}

	// appengine sdk requires us to set the auth header by hand
	u := url.UserPassword(c.Key, "")
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.String())))

	if data != nil {
		r.Method = "POST"
		r.Header.Set("Content-Type", "application/json")
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.Encode(data)
		r.Body = ioutil.NopCloser(&b)
	}

	return r
}

// Devices fetches a list of devices from PushBullet.
func (c *Client) Devices() ([]*Device, error) {
	req := c.buildRequest("/devices", nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errjson errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errjson)
		if err == nil {
			return nil, &errjson.ErrResponse
		}

		return nil, errors.New(resp.Status)
	}

	var devResp deviceResponse
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&devResp)
	if err != nil {
		return nil, err
	}

	for i := range devResp.Devices {
		devResp.Devices[i].Client = c
	}
	devices := append(devResp.Devices, devResp.SharedDevices...)
	return devices, nil
}

// Device fetches an device with a given nickname from PushBullet.
func (c *Client) Device(nickname string) (*Device, error) {
	devices, err := c.Devices()
	if err != nil {
		return nil, err
	}

	for i := range devices {
		if devices[i].Nickname == nickname {
			devices[i].Client = c
			return devices[i], nil
		}
	}
	return nil, ErrDeviceNotFound
}

// PushNote sends a note to the specific device with the given title and body
func (d *Device) PushNote(title, body string) error {
	return d.Client.PushNote(d.Iden, title, body)
}

// PushLink sends a link to the specific device with the given title and url
func (d *Device) PushLink(title, u, body string) error {
	return d.Client.PushLink(d.Iden, title, u, body)
}

// PushSMS sends an SMS to the specific user from the device with the given title and url
func (d *Device) PushSMS(deviceIden, phoneNumber, message string) error {
	return d.Client.PushSMS(d.Iden, deviceIden, phoneNumber, message)
}

// User represents the User object for pushbullet
type User struct {
	Iden            string      `json:"iden"`
	Email           string      `json:"email"`
	EmailNormalized string      `json:"email_normalized"`
	Created         float64     `json:"created"`
	Modified        float64     `json:"modified"`
	Name            string      `json:"name"`
	ImageUrl        string      `json:"image_url"`
	Preferences     interface{} `json:"preferences"`
}

// Me returns the user object for the pushbullet user
func (c *Client) Me() (*User, error) {
	req := c.buildRequest("/users/me", nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errjson errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errjson)
		if err == nil {
			return nil, &errjson.ErrResponse
		}

		return nil, errors.New(resp.Status)
	}

	var userResponse User
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&userResponse)
	if err != nil {
		return nil, err
	}
	return &userResponse, nil
}

type Push struct {
	Active                  bool     `json:"active"`
	Dismissed               bool     `json:"dismissed"`
	Iden                    string   `json:"iden"`
	Type                    string   `json:"type"`
	Created                 float64  `json:"created"`
	Modified                float64  `json:"modified"`
	GUID                    string   `json:"guid"`
	Direction               string   `json:"direction"`
	SenderIden              string   `json:"sender_iden"`
	SenderEmail             string   `json:"sender_email"`
	SenderEmailNormalized   string   `json:"sender_email_normalized"`
	SenderName              string   `json:"sender_name"`
	ReceiverIden            string   `json:"receiver_iden"`
	ReceiverEmail           string   `json:"receiver_email"`
	ReceiverEmailNormalized string   `json:"receiver_email_normalized"`
	TargetDeviceIden        string   `json:"target_device_iden"`
	SenderDeciveIden        string   `json:"sender_device_iden"`
	ClientIden              string   `json:"client_iden"`
	ChannelIden             string   `json:"channel_iden"`
	AwakeAppGUIDs           []string `json:"awake_app_guids"`
	Title                   string   `json:"title"`
	Body                    string   `json:"body"`
	URL                     string   `json:"url"`
	FileName                string   `json:"file_name"`
	FileMIME                string   `json:"file_type"`
	FileURL                 string   `json:"file_url"`
	ImageURL                string   `json:"image_url"`
	ImageHeight             int      `json:"image_height"`
	ImageWidth              int      `json:"image_width"`
}

func (c *Client) Pushes(after time.Time, activeOnly bool, cursor string, limit int) ([]*Push, error) {
	url := url.URL{
		Path: "/pushes",
	}
	q := url.Query()
	q.Add("modified_after", strconv.FormatInt(after.Unix(), 10))
	q.Add("active", strconv.FormatBool(activeOnly))
	if limit > 0 {
		q.Add("limit", strconv.FormatInt(int64(limit), 10))
	}
	if cursor != "" {
		q.Add("cursor", cursor)
	}
	url.RawQuery = q.Encode()
	fmt.Println(url.RequestURI())
	req := c.buildRequest(url.RequestURI(), nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResponse errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errResponse)
		if err == nil {
			return nil, &errResponse.ErrResponse
		}

		return nil, errors.New(resp.Status)
	}
	res := struct {
		Pushes []*Push `json:"pushes"`
	}{}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &res)
	if err != nil {
		return nil, err
	}
	return res.Pushes, nil
}

// Push pushes the data to a specific device registered with PushBullet.  The
// 'data' parameter is marshaled to JSON and sent as the request body.  Most
// users should call one of PusNote, PushLink, PushAddress, or PushList.
func (c *Client) Push(endPoint string, data interface{}) error {
	req := c.buildRequest(endPoint, data)
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResponse errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errResponse)
		if err == nil {
			return &errResponse.ErrResponse
		}

		return errors.New(resp.Status)
	}

	return nil
}

// Note exposes the required and optional fields of the Pushbullet push type=note
type Note struct {
	Iden  string `json:"device_iden,omitempty"`
	Tag   string `json:"channel_tag,omitempty"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// PushNote pushes a note with title and body to a specific PushBullet device.
func (c *Client) PushNote(iden string, title, body string) error {
	data := Note{
		Iden:  iden,
		Type:  "note",
		Title: title,
		Body:  body,
	}
	return c.Push("/pushes", data)
}

// PushNoteToChannel pushes a note with title and body to a specific PushBullet channel.
func (c *Client) PushNoteToChannel(tag string, title, body string) error {
	data := Note{
		Tag:   tag,
		Type:  "note",
		Title: title,
		Body:  body,
	}
	return c.Push("/pushes", data)
}

// Link exposes the required and optional fields of the Pushbullet push type=link
type Link struct {
	Iden  string `json:"device_iden,omitempty"`
	Tag   string `json:"channel_tag,omitempty"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Body  string `json:"body,omitempty"`
}

// PushLink pushes a link with a title and url to a specific PushBullet device.
func (c *Client) PushLink(iden, title, u, body string) error {
	data := Link{
		Iden:  iden,
		Type:  "link",
		Title: title,
		URL:   u,
		Body:  body,
	}
	return c.Push("/pushes", data)
}

// PushLinkToChannel pushes a link with a title and url to a specific PushBullet device.
func (c *Client) PushLinkToChannel(tag, title, u, body string) error {
	data := Link{
		Tag:   tag,
		Type:  "link",
		Title: title,
		URL:   u,
		Body:  body,
	}
	return c.Push("/pushes", data)
}

// EphemeralPush  exposes the required fields of the Pushbullet ephemeral object
type EphemeralPush struct {
	Type             string `json:"type"`
	PackageName      string `json:"package_name"`
	SourceUserIden   string `json:"source_user_iden"`
	TargetDeviceIden string `json:"target_device_iden"`
	ConversationIden string `json:"conversation_iden"`
	Message          string `json:"message"`
}

// Ephemeral constructs the Ephemeral object for pushing which requires the EphemeralPush object
type Ephemeral struct {
	Type string        `json:"type"`
	Push EphemeralPush `json:"push"`
}

// PushSMS sends an SMS message with pushbullet
func (c *Client) PushSMS(userIden, deviceIden, phoneNumber, message string) error {
	data := Ephemeral{
		Type: "push",
		Push: EphemeralPush{
			Type:             "messaging_extension_reply",
			PackageName:      "com.pushbullet.android",
			SourceUserIden:   userIden,
			TargetDeviceIden: deviceIden,
			ConversationIden: phoneNumber,
			Message:          message,
		},
	}
	return c.Push("/ephemerals", data)
}

// Subscription object allows interaction with pushbullet channels
type Subscription struct {
	Iden     string   `json:"iden"`
	Active   bool     `json:"active"`
	Created  float32  `json:"created"`
	Modified float32  `json:"modified"`
	Muted    string   `json:"muted"`
	Channel  *Channel `json:"channel"`
	Client   *Client  `json:"-"`
}

// Channel object contains specific information about the pushbullet Channel
type Channel struct {
	Iden        string `json:"iden"`
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageUrl    string `json:"image_url"`
	WebsiteUrl  string `json:"website_url"`
}

func (c *Client) Subscriptions() ([]*Subscription, error) {
	req := c.buildRequest("/subscriptions", nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errjson errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errjson)
		if err == nil {
			return nil, &errjson.ErrResponse
		}

		return nil, errors.New(resp.Status)
	}

	var subResp subscriptionResponse
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&subResp)
	if err != nil {
		return nil, err
	}

	for i := range subResp.Subscriptions {
		subResp.Subscriptions[i].Client = c
	}
	subscriptions := append(subResp.Subscriptions)
	return subscriptions, nil
}

// Subscription fetches an subscription with a given channel tag from PushBullet.
func (c *Client) Subscription(tag string) (*Subscription, error) {
	subs, err := c.Subscriptions()
	if err != nil {
		return nil, err
	}

	for i := range subs {
		if subs[i].Channel.Tag == tag {
			subs[i].Client = c
			return subs[i], nil
		}
	}
	return nil, ErrDeviceNotFound
}

// PushNote sends a note to the specific Channel with the given title and body
func (s *Subscription) PushNote(title, body string) error {
	return s.Client.PushNoteToChannel(s.Channel.Tag, title, body)
}

// PushNote sends a link to the specific Channel with the given title, url and body
func (s *Subscription) PushLink(title, u, body string) error {
	return s.Client.PushLinkToChannel(s.Channel.Tag, title, u, body)
}

package library

import (
	"crypto/tls"
	"encoding/json"
	"time"

	"code.google.com/p/go-imap/go1/imap"

	"github.com/nytlabs/streamtools/st/blocks"
	"github.com/nytlabs/streamtools/st/util"
)

// FromEmail holds channels we're going to use to communicate with streamtools,
// credentials for authenticating with an IMAP server and the IMAP client.
type FromEmail struct {
	blocks.Block
	queryrule chan chan interface{}
	inrule    chan interface{}
	out       chan interface{}
	quit      chan interface{}

	host     string
	username string
	password string
	mailbox  string

	client *imap.Client
}

type emailMessage struct {
	Received time.Time `json:"timestamp"`
	Body     string    `json:"email"`
}

// NewFromEmail is a simple factory for streamtools to make new blocks of this kind.
// By default, the block is configured for GMail.
func NewFromEmail() blocks.BlockInterface {
	return &FromEmail{host: "imap.gmail.com", mailbox: "INBOX"}
}

func newIMAPClient(host, username, password, mailbox string) (*imap.Client, error) {
	conn, err := imap.DialTLS(host, new(tls.Config))
	if err != nil {
		return conn, err
	}

	_, err = conn.Login(username, password)
	if err != nil {
		return conn, err
	}

	_, err = imap.Wait(conn.Select(mailbox, true))
	if err != nil {
		return conn, err
	}

	return conn, nil
}

func (e *FromEmail) idle() {
	var err error
	_, err = e.client.Idle()
	if err != nil {
		e.Error(err.Error())
		return
	}

	// kicks off occasional Data check during Idle
	poll := make(chan uint)
	poll <- 0

	// setup ticker to reset the idle every 20 minutes (RFC-2177 recommends every <=29 mins)
	reset := time.NewTicker(20 * time.Minute)

	for {
		err = nil
		select {
		case <-poll:
			// check pipe for new data
			err = e.client.Recv(0)
			if err != nil {
				e.Error(err.Error())
				sleep(poll)
				return
			}

			if len(e.client.Data) > 0 {
				// term idle and fetch unread
				_, err = e.client.IdleTerm()
				if err != nil {
					e.Error(err.Error())
					sleep(poll)
					return
				}

				// put any new unread messages on the channel
				err = e.fetchUnread()
				if err != nil {
					e.Error(err.Error())
					sleep(poll)
					return
				}

				// kick off that idle again
				_, err = e.client.Idle()
				if err != nil {
					e.Error(err.Error())
					sleep(poll)
					return
				}
			}
			// sleep a bit before checking the pipe again
			sleep(poll)

		case <-reset.C:
			_, err = e.client.IdleTerm()
			if err != nil {
				e.Error(err.Error())
				return
			}

			_, err = e.client.Idle()
			if err != nil {
				e.Error(err.Error())
				return
			}
		}
	}
}

func sleep(poll chan uint) {
	go func() {
		time.Sleep(10 * time.Second)
		poll <- 1
	}()
}

func (e *FromEmail) fetchUnread() error {
	cmd, err := findUnreadEmails(e.client)
	if err != nil {
		return err
	}

	var emails []emailMessage
	emails, err = getEmails(e.client, cmd)
	if err != nil {
		return err
	}

	for _, email := range emails {
		var emailStr []byte
		emailStr, err = json.Marshal(email)
		if err != nil {
			e.Error(err.Error())
			continue
		}
		e.out <- emailStr
	}

	return nil
}

func getEmails(client *imap.Client, cmd *imap.Command) ([]emailMessage, error) {
	var emails []emailMessage

	seq, _ := imap.NewSeqSet("")
	for _, rsp := range cmd.Data {
		uid := rsp.MessageInfo().UID
		seq.AddNum(uid)
	}

	fCmd, err := imap.Wait(client.UIDFetch(seq, "INTERNALDATE", "BODY[]", "UID", "RFC822.HEADER"))
	if err != nil {
		return emails, err
	}

	for _, msgData := range fCmd.Data {
		msgFields := msgData.MessageInfo().Attrs
		email := emailMessage{Received: imap.AsDateTime(msgFields["INTERNALDATE"]), Body: imap.AsString(msgFields["BODY[]"])}
		emails = append(emails, email)
	}
	return emails, nil
}

func findUnreadEmails(conn *imap.Client) (*imap.Command, error) {
	// get headers and UID for UnSeen message in src inbox...
	allMsgs, _ := imap.NewSeqSet("")
	allMsgs.Add("1:*")
	cmd, err := imap.Wait(conn.Fetch(allMsgs, "RFC822.HEADER", "UID"))
	if err != nil {
		return &imap.Command{}, err
	}

	return cmd, nil
}

// Setup is called once before running the block. We build up the channels and specify what kind of block this is.
func (e *FromEmail) Setup() {
	e.Kind = "FromEmail"
	e.out = e.Broadcast()
	e.inrule = e.InRoute("rule")
	e.queryrule = e.QueryRoute("rule")
	e.quit = e.Quit()
}

// parseAuthInRules will expect a payload from the inrules channel and
// attempt to pull the IMAP auth credentials out it.
func (e *FromEmail) parseAuthRules(msgI interface{}) error {
	var err error
	e.host, err = util.ParseRequiredString(msgI, "Host")
	if err != nil {
		return err
	}

	e.username, err = util.ParseRequiredString(msgI, "Username")
	if err != nil {
		return err
	}

	e.password, err = util.ParseRequiredString(msgI, "Password")
	if err != nil {
		return err
	}

	e.password, err = util.ParseRequiredString(msgI, "Mailbox")
	if err != nil {
		return err
	}

	return nil
}

// Run is the block's main loop. Here we listen on the different channels we set up.
func (e *FromEmail) Run() {
	var err error
	for {
		err = nil
		select {
		case msgI := <-e.inrule:
			// get id/pw/host/mailbox for IMAP
			err = e.parseAuthRules(msgI)
			if err != nil {
				e.Error(err.Error())
				continue
			}

			// initiate IMAP client with new creds
			e.client, err = newIMAPClient(e.host, e.username, e.password, e.mailbox)
			if err != nil {
				e.Error(err.Error())
				continue
			}
			defer e.client.Close(true)

			// do initial initial fetch on all existing unread messages
			err = e.fetchUnread()
			if err != nil {
				e.Error(err.Error())
				continue
			}

			// kick off idle in a goroutine
			go e.idle()

		case <-e.quit:
			e.client.Close(true)
			return
		case respChan := <-e.queryrule:
			// deal with a query request
			respChan <- map[string]interface{}{
				"Host":     e.host,
				"Username": e.username,
				"Password": e.password,
				"Mailbox":  e.mailbox,
			}
		}
	}
}

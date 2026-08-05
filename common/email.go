package common

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"slices"
	"strings"
	"time"
)

func ParseSingleEmailAddress(raw string) (string, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || address.Address == "" {
		return "", fmt.Errorf("invalid email address")
	}
	return address.Address, nil
}

func generateMessageID(sender string) (string, error) {
	at := strings.LastIndex(sender, "@")
	if at <= 0 || at == len(sender)-1 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), sender[at+1:]), nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

// EmailDeliveryConfigured reports whether outbound mail can be attempted at
// all. It mirrors the precondition SendEmail enforces, so callers can offer or
// hide email-dependent flows (verification, password recovery) instead of
// letting the user discover the gap only after the send fails.
func EmailDeliveryConfigured() bool {
	return SMTPServer != "" || SMTPAccount != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         SMTPServer,
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

// smtpDialTimeout bounds connection establishment; smtpSessionTimeout bounds
// the whole SMTP conversation so a stalled server cannot hold goroutines
// (and their callers) forever.
const (
	smtpDialTimeout    = 15 * time.Second
	smtpSessionTimeout = 60 * time.Second
)

func newSMTPClient(addr string) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	if SMTPSSLEnabled || (SMTPPort == 465 && !SMTPStartTLSEnabled) {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, smtpTLSConfig())
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(smtpSessionTimeout))
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(smtpSessionTimeout))
	client, err := smtp.NewClient(conn, SMTPServer)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if SMTPStartTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig()); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

func SendEmail(subject string, receiver string, content string) error {
	if SMTPFrom == "" { // for compatibility
		SMTPFrom = SMTPAccount
	}
	sender, err := ParseSingleEmailAddress(SMTPFrom)
	if err != nil {
		return err
	}
	recipient, err := ParseSingleEmailAddress(receiver)
	if err != nil {
		return err
	}
	id, err := generateMessageID(sender)
	if err != nil {
		return err
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return fmt.Errorf("SMTP 服务器未配置")
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	toHeader := (&mail.Address{Address: recipient}).String()
	fromHeader := (&mail.Address{Name: SystemName, Address: sender}).String()
	message := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		toHeader, fromHeader, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	auth := getSMTPAuth()
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	client, err := newSMTPClient(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if shouldAuthenticateSMTP() {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(sender); err != nil {
		return err
	}
	if err = client.Rcpt(recipient); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(message)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	err = client.Quit()
	if err != nil {
		SysError(fmt.Sprintf("SMTP client QUIT failed: %v", err))
	}
	return err
}

package fyneui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/network/tor"
	log "github.com/sirupsen/logrus"
)

// The screens between a start and the main user interface, for an install that does not open with nothing asked:
// the password, the recovery code, and the ones for an install that cannot be opened at all.  They are built when
// they are shown, since most starts show none of them, and they replace each other as the window's content.

const lockedScreenWidth = float32(420)

const recoveryForForgottenPassword = "Enter your recovery code to open Bounce again.  You can choose a new password, or leave it empty to have none."
const recoveryForLostKeystoreSecret = "This device's keystore no longer holds the key to Bounce's data.  That happens when the system is reinstalled, or when the Bounce folder is moved to another device or user.  Enter your recovery code to open Bounce again."

const deadEndWithoutRecoveryCode = "Without a recovery code, the data Bounce keeps on this device cannot be opened."
const deadEndKeyFileMissing = "The file that holds the key to Bounce's data, bounce.key, is missing from this device, and without it the data cannot be opened.  If you have a backup of the Bounce folder, quit and put that file back."
const deadEndKeyFileDamaged = "The file that holds the key to Bounce's data, bounce.key, is damaged, and without it the data cannot be opened.  If you have a backup of the Bounce folder, quit and put that file back."

// lockedScreen lays out one of these screens: the logo above a column narrow enough to read
func lockedScreen(title string, content ...fyne.CanvasObject) fyne.CanvasObject {
	column := container.NewVBox(widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}))
	for _, object := range content {
		column.Add(object)
	}
	return container.NewCenter(
		container.NewVBox(
			container.NewCenter(makeLogo(228, 167)),
			container.New(newMinWidthLayout(lockedScreenWidth), column),
		),
	)
}

func lockedText(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}

// lockedMessage is the line a screen uses to say what was wrong with the last attempt, hidden while there is nothing
// to say
func lockedMessage(text string) *widget.Label {
	label := lockedText(text)
	label.Importance = widget.DangerImportance
	if text == "" {
		label.Hide()
	}
	return label
}

// open tries to open the install, with the password the user typed or an empty one when none has been asked for.
// Stretching a password and moving a database to a new key both take long enough to notice, so it happens away from
// the UI thread, behind a screen that says so.
func (ui *ui) open(password string) {
	ui.window.SetContent(lockedScreen("opening..."))
	go func() {
		engine, err := chat.Open(ui, &tor.Network{}, ui.platform, password)
		fyne.Do(func() {
			if err != nil {
				ui.showOpenError(err, password)
				return
			}
			ui.engineOpened(engine)
			if ui.state.focused {
				ui.enteredForeground()
			}
		})
	}()
}

// showOpenError shows the screen that goes with the reason the install did not open.  password is what the attempt
// was made with, for the screens that try again.
func (ui *ui) showOpenError(err error, password string) {
	switch {
	case errors.Is(err, chat.ErrPasswordRequired):
		ui.showUnlock("")
	case errors.Is(err, chat.ErrWrongPassword):
		ui.showUnlock("That password is not right.")
	case errors.Is(err, chat.ErrKeystoreSecretMissing):
		ui.showRecovery(recoveryForLostKeystoreSecret, nil)
	case errors.Is(err, chat.ErrKeystoreUnavailable):
		ui.showOpenFailed(
			"Bounce can't reach this device's keystore",
			"The key to Bounce's data is kept in this device's keystore, which did not answer.  Unlock your keyring, or sign in to this device again, and then try again.",
			password,
		)
	case errors.Is(err, chat.ErrKeyFileMissing):
		ui.showDeadEnd(deadEndKeyFileMissing, nil)
	case errors.Is(err, chat.ErrKeyFileDamaged):
		ui.showDeadEnd(deadEndKeyFileDamaged, nil)
	default:
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("Bounce could not be opened")
		ui.showOpenFailed("Bounce could not be opened", err.Error(), password)
	}
}

func (ui *ui) showUnlock(message string) {
	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Password")
	passwordEntry.OnSubmitted = ui.open

	unlockButton := widget.NewButton("Unlock", func() {
		ui.open(passwordEntry.Text)
	})
	unlockButton.Importance = widget.HighImportance
	forgotButton := widget.NewButton("Forgot Password?", func() {
		ui.showRecovery(recoveryForForgottenPassword, func() {
			ui.showUnlock("")
		})
	})
	forgotButton.Importance = widget.LowImportance

	ui.window.SetContent(lockedScreen(
		"Bounce is locked",
		lockedMessage(message),
		passwordEntry,
		unlockButton,
		forgotButton,
	))
	ui.window.Canvas().Focus(passwordEntry)
}

// showRecovery asks for the recovery code and the password to use from now on.  back returns to the screen the
// user came from, and is nil when recovery is the only way on.
func (ui *ui) showRecovery(explanation string, back func()) {
	message := lockedMessage("")
	codeEntry := widget.NewEntry()
	codeEntry.SetPlaceHolder("Recovery code")
	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("New password (optional)")
	confirmEntry := widget.NewPasswordEntry()
	confirmEntry.SetPlaceHolder("Confirm new password")

	var recoverButton *widget.Button
	recoverButton = widget.NewButton("Recover", func() {
		if passwordEntry.Text != confirmEntry.Text {
			message.SetText("The two passwords are not the same.")
			message.Show()
			return
		}
		code := codeEntry.Text
		password := passwordEntry.Text
		message.Hide()
		recoverButton.Disable()
		go func() {
			err := chat.Recover(ui.platform, code, password)
			fyne.Do(func() {
				switch {
				case err == nil:
					ui.showRecovered(password)
				case errors.Is(err, chat.ErrNoRecoveryCode):
					ui.showDeadEnd("No recovery code was ever created for this install.  "+deadEndWithoutRecoveryCode, nil)
				case errors.Is(err, chat.ErrMistypedRecoveryCode):
					message.SetText("That code has a typing mistake in it.  Check it against the one you wrote down.")
					message.Show()
					recoverButton.Enable()
				case errors.Is(err, chat.ErrWrongRecoveryCode):
					message.SetText("That is not the recovery code for this install.  Only the last code created for it works.")
					message.Show()
					recoverButton.Enable()
				default:
					ui.showOpenError(err, "")
				}
			})
		}()
	})
	recoverButton.Importance = widget.HighImportance
	noCodeButton := widget.NewButton("I Don't Have a Recovery Code", func() {
		ui.showDeadEnd(deadEndWithoutRecoveryCode, func() {
			ui.showRecovery(explanation, back)
		})
	})
	noCodeButton.Importance = widget.LowImportance

	content := []fyne.CanvasObject{
		lockedText(explanation),
		message,
		codeEntry,
		passwordEntry,
		confirmEntry,
		recoverButton,
		noCodeButton,
	}
	if back != nil {
		backButton := widget.NewButton("Back", back)
		backButton.Importance = widget.LowImportance
		content = append(content, backButton)
	}
	ui.window.SetContent(lockedScreen("Recover Bounce", content...))
	ui.window.Canvas().Focus(codeEntry)
}

// showRecovered confirms a recovery and opens the install with the password it was given
func (ui *ui) showRecovered(password string) {
	text := "The data Bounce keeps on this device can be opened again.  No password is set."
	if password != "" {
		text = "The data Bounce keeps on this device can be opened again.  From now on Bounce asks for your new password when it starts."
	}
	okButton := widget.NewButton("OK", func() {
		ui.open(password)
	})
	okButton.Importance = widget.HighImportance

	ui.window.SetContent(lockedScreen(
		"Bounce is recovered",
		lockedText(text),
		lockedText("How this install is protected now, and whether your recovery code still works, is shown under Security in the settings."),
		okButton,
	))
}

// showDeadEnd is for an install whose data cannot be opened by anything the user has.  What is left is to start
// over on this device.  back returns to the screen the user came from, and is nil when there is none.
func (ui *ui) showDeadEnd(reason string, back func()) {
	resetButton := widget.NewButton("Reset Bounce on This Device", func() {
		confirmReset := dialog.NewConfirm(
			"Reset Bounce on this device?",
			"Everything Bounce keeps on this device will be deleted,\nand it cannot be brought back.",
			func(reset bool) {
				if !reset {
					return
				}
				err := chat.Reset(ui.platform)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error resetting the install")
					ui.showOpenFailed("Bounce could not be reset", err.Error(), "")
					return
				}
				ui.open("")
			},
			ui.window,
		)
		confirmReset.SetConfirmText("Reset")
		confirmReset.SetDismissText("Cancel")
		confirmReset.SetConfirmImportance(widget.DangerImportance)
		confirmReset.Show()
	})
	resetButton.Importance = widget.DangerImportance

	content := []fyne.CanvasObject{
		lockedText(reason),
		lockedText("Resetting deletes the messages, files, contacts and identity Bounce keeps on this device, and starts over as a new install.  Your other devices are not changed."),
		resetButton,
	}
	if back != nil {
		backButton := widget.NewButton("Back", back)
		backButton.Importance = widget.LowImportance
		content = append(content, backButton)
	} else {
		quitButton := widget.NewButton("Quit", ui.Quit)
		quitButton.Importance = widget.LowImportance
		content = append(content, quitButton)
	}
	ui.window.SetContent(lockedScreen("Bounce can't open its data", content...))
}

// showOpenFailed is for a failure the user may be able to clear, such as a keystore that is locked, with the same
// attempt behind its button
func (ui *ui) showOpenFailed(title string, reason string, password string) {
	retryButton := widget.NewButton("Try Again", func() {
		ui.open(password)
	})
	retryButton.Importance = widget.HighImportance
	quitButton := widget.NewButton("Quit", ui.Quit)
	quitButton.Importance = widget.LowImportance

	ui.window.SetContent(lockedScreen(
		title,
		lockedText(reason),
		retryButton,
		quitButton,
	))
}

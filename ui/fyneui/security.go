package fyneui

import (
	"errors"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

// How the install is protected on this device, and the user's side of it: the password, the recovery code, and the
// one banner that suggests a recovery code to somebody who never opens the settings

const recoveryBannerDismissed = "recoveryBannerDismissed"

const securityDialogWidth = float32(420)

const recoveryCodeExplanation = "A recovery code opens Bounce if you forget your password, or if this device's keystore loses the key to Bounce's data.  It is shown once, to be written down and kept somewhere safe."

// securityErrorText is what the user is told when a change to how the install is protected was refused
func securityErrorText(err error) string {
	switch {
	case errors.Is(err, chat.ErrWrongPassword):
		return "That password is not right."
	case errors.Is(err, chat.ErrPasswordRequired):
		return "Enter your password."
	case errors.Is(err, chat.ErrKeystoreUnavailable):
		return "This device's keystore could not be reached.  Unlock it and try again."
	case errors.Is(err, chat.ErrKeystoreSecretMissing):
		return "This device's keystore no longer holds the key to Bounce's data."
	case errors.Is(err, chat.ErrRollUnfinished):
		return "Bounce has to be restarted before a password can be set again."
	}
	return err.Error()
}

// buildSecurity makes the security section of the settings and the recovery code banner of the main view.  Both
// are filled in by refreshSecurity.
func (ui *ui) buildSecurity() {
	ui.widgets.settings.securityStatus = widget.NewLabel("")
	ui.widgets.settings.setPassword = widget.NewButton("Set Password", ui.showSetPassword)
	ui.widgets.settings.removePassword = widget.NewButton("Remove Password", ui.showRemovePassword)
	ui.widgets.settings.recoveryCodeStatus = widget.NewLabel("")
	ui.widgets.settings.createRecoveryCode = widget.NewButton("Create Recovery Code", ui.showCreateRecoveryCode)

	ui.widgets.recoveryBannerText = widget.NewLabel("")
	createButton := widget.NewButton("Create Recovery Code", ui.showCreateRecoveryCode)
	dismissButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		ui.app.Preferences().SetBool(recoveryBannerDismissed, true)
		ui.containers.recoveryBanner.Hide()
	})
	dismissButton.Importance = widget.LowImportance
	ui.containers.recoveryBanner = container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(createButton, dismissButton), ui.widgets.recoveryBannerText),
		widget.NewSeparator(),
	)
	ui.containers.recoveryBanner.Hide()
}

// refreshSecurity brings the security section and the banner in line with how the install is protected now
func (ui *ui) refreshSecurity() {
	status, err := ui.bounce.SecurityStatus()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error reading how the install is protected")
		return
	}
	ui.state.security = status
	protected := status.KeystoreUsed || status.PasswordSet

	switch {
	case status.KeystoreUsed && status.PasswordSet:
		ui.widgets.settings.securityStatus.SetText("Protected by this device and your password")
	case status.KeystoreUsed:
		ui.widgets.settings.securityStatus.SetText("Protected by this device")
	case status.PasswordSet:
		ui.widgets.settings.securityStatus.SetText("Protected by your password")
	default:
		ui.widgets.settings.securityStatus.SetText("Not protected on this device")
	}

	if status.PasswordSet {
		ui.widgets.settings.setPassword.SetText("Change Password")
		ui.widgets.settings.removePassword.Show()
	} else {
		ui.widgets.settings.setPassword.SetText("Set Password")
		ui.widgets.settings.removePassword.Hide()
	}

	switch {
	case !protected:
		ui.widgets.settings.recoveryCodeStatus.SetText("A recovery code can be created once a password is set")
		ui.widgets.settings.createRecoveryCode.Hide()
	case status.RecoveryCodeCreatedAt == 0:
		ui.widgets.settings.recoveryCodeStatus.SetText("Recovery code: none")
		ui.widgets.settings.createRecoveryCode.SetText("Create Recovery Code")
		ui.widgets.settings.createRecoveryCode.Show()
	default:
		created := time.Unix(status.RecoveryCodeCreatedAt, 0).Format("2 Jan 2006")
		ui.widgets.settings.recoveryCodeStatus.SetText("Recovery code: created " + created)
		ui.widgets.settings.createRecoveryCode.SetText("Create New Recovery Code")
		ui.widgets.settings.createRecoveryCode.Show()
	}

	// The banner is for an install that has something to lose and somebody to lose it with: protected, with no
	// recovery code, and with a first contact made
	if status.PasswordSet {
		ui.widgets.recoveryBannerText.SetText("A recovery code lets you back into Bounce if you forget your password.")
	} else {
		ui.widgets.recoveryBannerText.SetText("A recovery code lets you back into Bounce if this device's keystore loses its key.")
	}
	if protected && status.RecoveryCodeCreatedAt == 0 && len(ui.users.alphabetized()) > 1 && !ui.app.Preferences().Bool(recoveryBannerDismissed) {
		ui.containers.recoveryBanner.Show()
	} else {
		ui.containers.recoveryBanner.Hide()
	}
}

// securityDialog lays out the content of one of the dialogs below, in a column wide enough for a sentence to wrap in
func securityDialog(content ...fyne.CanvasObject) fyne.CanvasObject {
	return container.New(newMinWidthLayout(securityDialogWidth), container.NewVBox(content...))
}

func (ui *ui) showSetPassword() {
	before := ui.state.security
	var setPasswordDialog dialog.Dialog

	message := lockedMessage("")
	currentEntry := widget.NewPasswordEntry()
	currentEntry.SetPlaceHolder("Current password")
	if !before.PasswordSet {
		currentEntry.Hide()
	}
	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("New password")
	confirmEntry := widget.NewPasswordEntry()
	confirmEntry.SetPlaceHolder("Confirm new password")

	var saveButton *widget.Button
	saveButton = widget.NewButton("Set Password", func() {
		if passwordEntry.Text == "" {
			message.SetText("Enter a new password.")
			message.Show()
			return
		}
		if passwordEntry.Text != confirmEntry.Text {
			message.SetText("The two passwords are not the same.")
			message.Show()
			return
		}
		current := currentEntry.Text
		password := passwordEntry.Text
		message.Hide()
		saveButton.Disable()
		go func() {
			err := ui.bounce.SetPassword(current, password)
			fyne.Do(func() {
				if err != nil {
					message.SetText(securityErrorText(err))
					message.Show()
					saveButton.Enable()
					return
				}
				setPasswordDialog.Hide()
				ui.refreshSecurity()
				ui.showPasswordSet(before, password)
			})
		}()
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		setPasswordDialog.Hide()
	})

	title := "Set Password"
	if before.PasswordSet {
		title = "Change Password"
	}
	setPasswordDialog = dialog.NewCustomWithoutButtons(
		title,
		securityDialog(
			lockedText("Bounce asks for this password every time it starts.  If you forget it and have no recovery code, the data Bounce keeps on this device cannot be opened."),
			message,
			currentEntry,
			passwordEntry,
			confirmEntry,
			container.NewGridWithColumns(2, cancelButton, saveButton),
		),
		ui.window,
	)
	ui.showDialog(setPasswordDialog, nil)
}

// showPasswordSet confirms a new password, and offers a recovery code to an install that has none.  before is how
// the install was protected until now.
func (ui *ui) showPasswordSet(before chat.SecurityStatus, password string) {
	title := "Password Set"
	text := "Bounce asks for your password from the next start on."
	if before.PasswordSet {
		title = "Password Changed"
		text = "Bounce asks for your new password from the next start on."
	}
	if !before.KeystoreUsed && !before.PasswordSet {
		// The first protection this install has had, which the engine finishes at the next start by moving
		// everything to a new master key
		text = "Bounce asks for your password from the next start on.  That start also finishes protecting the data already on this device, so restart Bounce when you can."
	}
	if ui.state.security.RecoveryCodeCreatedAt != 0 {
		ui.showDialog(dialog.NewCustom(title, "OK", securityDialog(lockedText(text)), ui.window), nil)
		return
	}

	ui.showDialog(
		dialog.NewCustomConfirm(
			title,
			"Create Recovery Code",
			"Not Now",
			securityDialog(lockedText(text), lockedText(recoveryCodeExplanation)),
			func(create bool) {
				if !create {
					return
				}
				go func() {
					code, err := ui.bounce.CreateRecoveryCode(password)
					fyne.Do(func() {
						if err != nil {
							ui.showDialog(dialog.NewError(errors.New("Error creating a recovery code: "+securityErrorText(err)), ui.window), nil)
							return
						}
						ui.refreshSecurity()
						ui.showRecoveryCode(code)
					})
				}()
			},
			ui.window,
		),
		nil,
	)
}

func (ui *ui) showRemovePassword() {
	var removePasswordDialog dialog.Dialog

	text := "Bounce will open without asking for a password, with its data protected by this device alone."
	if !ui.state.security.KeystoreUsed {
		text = "Nothing will protect the data Bounce keeps on this device, and your recovery code will stop working."
	}
	message := lockedMessage("")
	currentEntry := widget.NewPasswordEntry()
	currentEntry.SetPlaceHolder("Current password")

	var removeButton *widget.Button
	removeButton = widget.NewButton("Remove Password", func() {
		current := currentEntry.Text
		message.Hide()
		removeButton.Disable()
		go func() {
			err := ui.bounce.SetPassword(current, "")
			fyne.Do(func() {
				if err != nil {
					message.SetText(securityErrorText(err))
					message.Show()
					removeButton.Enable()
					return
				}
				removePasswordDialog.Hide()
				ui.refreshSecurity()
			})
		}()
	})
	removeButton.Importance = widget.DangerImportance
	cancelButton := widget.NewButton("Cancel", func() {
		removePasswordDialog.Hide()
	})

	removePasswordDialog = dialog.NewCustomWithoutButtons(
		"Remove Password",
		securityDialog(
			lockedText(text),
			message,
			currentEntry,
			container.NewGridWithColumns(2, cancelButton, removeButton),
		),
		ui.window,
	)
	ui.showDialog(removePasswordDialog, nil)
}

func (ui *ui) showCreateRecoveryCode() {
	status := ui.state.security
	var createDialog dialog.Dialog

	text := recoveryCodeExplanation
	if status.RecoveryCodeCreatedAt != 0 {
		text = recoveryCodeExplanation + "  The code you have now will stop working."
	}
	message := lockedMessage("")
	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Password")
	if !status.PasswordSet {
		passwordEntry.Hide()
	}

	var createButton *widget.Button
	createButton = widget.NewButton("Create Recovery Code", func() {
		password := passwordEntry.Text
		message.Hide()
		createButton.Disable()
		go func() {
			code, err := ui.bounce.CreateRecoveryCode(password)
			fyne.Do(func() {
				if err != nil {
					message.SetText(securityErrorText(err))
					message.Show()
					createButton.Enable()
					return
				}
				createDialog.Hide()
				ui.refreshSecurity()
				ui.showRecoveryCode(code)
			})
		}()
	})
	createButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		createDialog.Hide()
	})

	createDialog = dialog.NewCustomWithoutButtons(
		"Recovery Code",
		securityDialog(
			lockedText(text),
			message,
			passwordEntry,
			container.NewGridWithColumns(2, cancelButton, createButton),
		),
		ui.window,
	)
	ui.showDialog(createDialog, nil)
}

// showRecoveryCode shows a recovery code the one time it can be shown, as two rows of four groups
func (ui *ui) showRecoveryCode(code string) {
	var codeDialog dialog.Dialog

	rows := container.NewVBox()
	groups := strings.Split(code, "-")
	for len(groups) > 0 {
		row := groups[:min(4, len(groups))]
		groups = groups[len(row):]
		rows.Add(widget.NewRichText(&widget.TextSegment{
			Text: strings.Join(row, "-"),
			Style: widget.RichTextStyle{
				Alignment: fyne.TextAlignCenter,
				SizeName:  theme.SizeNameSubHeadingText,
				TextStyle: fyne.TextStyle{Monospace: true},
			},
		}))
	}
	copyButton := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		ui.app.Clipboard().SetContent(code)
	})
	doneButton := widget.NewButton("I Have Written It Down", func() {
		codeDialog.Hide()
	})
	doneButton.Importance = widget.HighImportance

	codeDialog = dialog.NewCustomWithoutButtons(
		"Your Recovery Code",
		securityDialog(
			lockedText("Write this code down and keep it somewhere safe.  It is not shown again, and anyone who has it and this device can open your messages."),
			rows,
			container.NewGridWithColumns(2, copyButton, doneButton),
		),
		ui.window,
	)
	ui.showDialog(codeDialog, nil)
}

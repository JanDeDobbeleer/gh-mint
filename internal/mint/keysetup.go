package mint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func encryptSetupKey(dir string, cfg config, prompts keyPrompter, stdout io.Writer, update bool) error {
	if update {
		block, err := readKeyBlock(cfg.KeyFile)
		if err != nil {
			return err
		}
		encrypted := block.Type == "ENCRYPTED PRIVATE KEY"
		clear(block.Bytes)
		if encrypted {
			_, err := fmt.Fprintln(stdout, "The configured private key is already encrypted.")
			return err
		}
	}
	original, err := os.Stat(cfg.KeyFile)
	if err != nil {
		return errors.New("cannot inspect the original private key")
	}
	key, err := readKey(cfg.KeyFile, prompts.password)
	if err != nil {
		return err
	}
	password, err := prompts.password("New private key passphrase: ")
	defer clear(password)
	if err != nil {
		return err
	}
	if len(password) == 0 {
		return errors.New("private key passphrase cannot be empty")
	}
	confirmation, err := prompts.password("Confirm private key passphrase: ")
	defer clear(confirmation)
	if err != nil {
		return err
	}
	if !bytes.Equal(password, confirmation) {
		return errors.New("private key passphrases do not match")
	}
	path, err := writeEncryptedKey(dir, key, password)
	clear(password)
	clear(confirmation)
	if err != nil {
		return err
	}
	configured := false
	defer func() {
		if !configured {
			_ = os.Remove(path)
		}
	}()
	source := cfg.KeyFile
	if update {
		err = updateKeyFile(dir, cfg, path)
	} else {
		cfg.KeyFile = path
		err = writeConfig(dir, cfg)
	}
	if err != nil {
		return err
	}
	configured = true
	if _, err := fmt.Fprintln(stdout, "Configured gh-mint with a verified encrypted private key. Launches will ask for its passphrase."); err != nil {
		return err
	}
	remove, err := prompts.confirm(fmt.Sprintf("Delete the original private key at %q? Backups are not erased. Type yes to delete [no]: ", source))
	if err != nil {
		return errors.New("encrypted key configured, but deletion was not confirmed; original key retained")
	}
	if !remove {
		_, err := fmt.Fprintln(stdout, "Original private key retained. Store it securely or remove it yourself.")
		return err
	}
	current, err := os.Lstat(source)
	if err != nil || !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) {
		return errors.New("encrypted key configured, but original key changed during setup; it was not deleted")
	}
	if err := os.Remove(source); err != nil {
		return errors.New("encrypted key configured, but the original could not be deleted; remove it yourself")
	}
	_, err = fmt.Fprintln(stdout, "Original private key deleted. Copies in backups or other locations are not erased.")
	return err
}

func updateKeyFile(dir string, expected config, path string) error {
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return errors.New("cannot read the configuration to update its key")
	}
	var current config
	if json.Unmarshal(data, &current) != nil || current != expected {
		return errors.New("configuration changed during key encryption; it was not overwritten")
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil {
		return errors.New("invalid gh-mint configuration")
	}
	values["key_file"], err = json.Marshal(path)
	if err != nil {
		return err
	}
	data, err = json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return errors.New("cannot stage the encrypted key configuration")
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("cannot write the encrypted key configuration")
	}
	if err := os.Rename(file.Name(), configPath); err != nil {
		return errors.New("cannot replace the encrypted key configuration")
	}
	return nil
}

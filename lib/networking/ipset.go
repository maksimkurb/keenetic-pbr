package networking

import (
	"fmt"
	"github.com/maksimkurb/keenetic-pbr/lib/config"
	"github.com/maksimkurb/keenetic-pbr/lib/log"
	"net/netip"
	"os/exec"
)

const ipsetCommand = "ipset"

// CreateIpset creates a new ipset with the given name and IP family (4 or 6)
func CreateIpset(ipset *config.IPSetConfig) error {
	// Determine IP family
	family := "inet"
	if ipset.IPVersion == 6 {
		family = "inet6"
	} else if ipset.IPVersion != 0 && ipset.IPVersion != 4 {
		log.Warnf("unknown IP version %d, assuming IPv4", ipset.IPVersion)
	}

	cmd := exec.Command(ipsetCommand, "create", ipset.IPSetName, "hash:net", "family", family, "-exist")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create ipset %s (IPv%d): %v", ipset.IPSetName, ipset.IPVersion, err)
	}

	return nil
}

// CheckIpsetExists checks if the given ipset exists
func CheckIpsetExists(ipset *config.IPSetConfig) (bool, error) {
	cmd := exec.Command(ipsetCommand, "-n", "list", ipset.IPSetName)
	if err := cmd.Start(); err != nil {
		return false, err
	}

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			return exiterr.ExitCode() == 0, nil
		} else {
			return false, err
		}
	} else {
		return true, nil
	}
}

// AddToIpset adds the given networks to the specified ipset
func AddToIpset(ipset *config.IPSetConfig, networks []netip.Prefix) error {
	if _, err := exec.LookPath(ipsetCommand); err != nil {
		return fmt.Errorf("failed to find ipset command %s: %v", ipsetCommand, err)
	}

	cmd := exec.Command(ipsetCommand, "restore", "-exist")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if err := stdin.Close(); err != nil {
				errCh <- fmt.Errorf("failed to close stdin pipe: %v", err)
			}
			close(errCh) // Close the channel when the goroutine finishes
		}()

		// Write commands to stdin
		if ipset.FlushBeforeApplying {
			if _, err := fmt.Fprintf(stdin, "flush %s\n", ipset.IPSetName); err != nil {
				log.Warnf("failed to flush ipset %s: %v", ipset.IPSetName, err)
			}
		}

		errorCounter := 0
		for _, network := range networks {
			if !network.IsValid() {
				log.Warnf("skipping invalid network %v", network)
				continue
			}
			if _, err := fmt.Fprintf(stdin, "add %s %s\n", ipset.IPSetName, network.String()); err != nil {
				log.Warnf("failed to add address %s to ipset %s: %v", network, ipset.IPSetName, err)
				errorCounter++

				if errorCounter > 10 {
					errCh <- fmt.Errorf("too many errors, aborting import")
					return
				}
			}
		}
	}()

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add addresses to ipset %s: %v\n%s", ipset.IPSetName, err, output)
	}

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

// Package gpio provides GPIO input reading with hardware abstraction.
// The real implementation uses the Linux GPIO character device.
// The fake implementation allows testing without hardware.
//
// GPIO signal inversion lives here and nowhere else: each channel's ActiveLow
// flag (from the sensor profile) decides whether a raw active line means
// logical ON or OFF.
package gpio

// ChannelReader reads N GPIO inputs in logical form, one bool per configured
// channel, aligned to the profile's channel order. This is the generic,
// sensor-agnostic reader interface.
type ChannelReader interface {
	// ReadChannels returns one logical (post-inversion) value per channel,
	// in profile order.
	ReadChannels() ([]bool, error)
	// Close releases GPIO resources.
	Close() error
}

package main

// shutdownCh is closed when the process should gracefully shut down.
// On Linux it is triggered by SIGINT/SIGTERM; on Windows by the SCM
// service handler. The signal-handling goroutine and the server loop
// select on this channel.
var shutdownCh = make(chan struct{})

// shutdownDoneCh is closed after the server has completed its graceful
// shutdown, including closing the metrics store and recovery loop.
var shutdownDoneCh = make(chan struct{})

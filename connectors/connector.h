#ifndef CONNECTOR_H
#define CONNECTOR_H

static int header_length = 4;
static char IP[16] = "127.0.0.1";
static int PORT = 8000;
static int running = 0;
enum error_codes {
  GOOD,
  CREATION,
  ADDR,
  CONNECTION,
  ERROR,
  CLOSED,
  RUNNING,
};
static const char *errors[] = {
  "",
  "socket creation error",
  "invalid address/address not supported",
  "connection failed",
  "error", // Be more descriptive?
  "closed",
  "already running",
};

void changeIP(const char *ip);
void changePort(int port);
const char* start(const char *route);
void stop(void);

#endif

#ifndef CONNECTOR_H
#define CONNECTOR_H

static int header_length = 4;
static char PROXY_IP[16] = "127.0.0.1";
static int PROXY_PORT = 8000;
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

void change_proxy_ip(const char *ip);
void change_proxy_port(int port);
const char* start(const char *route, const char *addr);
void stop(void);

#endif

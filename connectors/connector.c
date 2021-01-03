#include "connector.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>

void changeIP(const char *ip) {
  strcpy(IP, ip);
}

void changePort(int port) {
  PORT = port;
}

const char* start(const char *route) {
  if (running)
    return errors[RUNNING];

  int sock = 0;
  struct sockaddr_in serv_addr;
  if ((sock = socket(AF_INET, SOCK_STREAM, 0)) < 0)
    return errors[CREATION];

  serv_addr.sin_family = AF_INET;
  // Convert port
  serv_addr.sin_port = htons(PORT);

  // Convert IPv4 and IPv6 addresses from text to binary form
  if (inet_pton(AF_INET, IP, &serv_addr.sin_addr) <= 0)
    return errors[ADDR];

  if (connect(sock, (struct sockaddr *)&serv_addr, sizeof(serv_addr)) < 0)
    return errors[CONNECTION];

  int valread;
  char buffer[1024] = {0};
  
  if (send(sock, route, strlen(route), 0) == -1)
    return errors[ERROR];

  if ((valread = recv(sock, buffer, 1024, 0)) == -1)
    return errors[ERROR];

  running = 1;
  while (running) {
    if ((valread = recv(sock, buffer, header_length, 0)) == -1) {
      running = 0;
      return errors[ERROR];
    } else if (!valread || !strcmp(buffer, "closed") || !strcmp(buffer, "error")) {
      running = 0;
      return errors[CLOSED];
    }
    int length = atoi(buffer);

    if ((valread = recv(sock, buffer, length, 0)) == -1) {
      running = 0;
      return errors[ERROR];
    } else if (!valread || !strcmp(buffer, "closed") || !strcmp(buffer, "error")) {
      running = 0;
      return errors[CLOSED];
    }

    if (send(sock, route, strlen(route), 0) <= 0) {
      running = 0;
      return errors[ERROR];
    }
  }

  // Send the closing message to the proxy and close the socket
  send(sock, "closed", 6, 0);
  shutdown(sock, SHUT_RDWR);
  return errors[GOOD];
}

void stop() {
  running = 0;
}
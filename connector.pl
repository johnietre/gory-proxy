#!/usr/bin/perl
use strict;
use Socket;

if ((scalar @ARGV) != 1) {
  die "Must provide (only) URL\n";
}

my $url = $ARGV[0];
unless ($url =~ m<https?://[\w\.:]+/(\w+)/?>) { # $1 is the slug
  die "Invalid URL: must be properly formatted with a slug\n";
}

my $ip = "localhost";
my $port = 9999;
socket(SOCKET, PF_INET, SOCK_STREAM, (getprotobyname('tcp'))[2])
  or die "Error creating connection\n";
connect(SOCKET, pack_sockaddr_in($port, inet_aton($ip)))
  or die "Error connecting to proxy\n";

close SOCKET;

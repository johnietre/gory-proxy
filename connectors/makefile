python:
	swig -python connector.i
	gcc -c connector.c connector_wrap.c -I/usr/bin/python3
	ld -shared connector.o connector_wrap.o -o _connector.so
CC ?= cc
CFLAGS ?= -O2
HARDEN = -std=c11 -Wall -Wextra -Wpedantic -Werror -D_FORTIFY_SOURCE=2 -fstack-protector-strong -fPIE
LDFLAGS += -pie -Wl,-z,relro,-z,now
DESTDIR ?=

.PHONY: all clean test check-local install uninstall
all: arootd aroot-client aroot-admin
arootd: aroot.c
	$(CC) $(CPPFLAGS) $(CFLAGS) $(HARDEN) $< $(LDFLAGS) -o $@
aroot-client aroot-admin: arootd
	cp arootd $@
arootd-test: aroot.c
	$(CC) $(CPPFLAGS) $(CFLAGS) $(HARDEN) -DAROOT_TESTING $< $(LDFLAGS) -o $@
aroot-client-test: arootd-test
	cp arootd-test $@
unit-tests: unit-tests.c aroot.c
	$(CC) $(CPPFLAGS) $(CFLAGS) $(HARDEN) $< $(LDFLAGS) -o $@
check-local: all arootd-test aroot-client-test unit-tests
	./unit-tests
	python3 tests.py Installation
test: all arootd-test aroot-client-test unit-tests
	./unit-tests
	python3 tests.py
install: all
	install -d -m 0755 "$(DESTDIR)/usr/libexec" "$(DESTDIR)/usr/bin" "$(DESTDIR)/usr/sbin"
	install -m 0755 arootd aroot-client "$(DESTDIR)/usr/libexec/"
	install -m 0755 aroot-admin "$(DESTDIR)/usr/sbin/"
	install -m 0755 aroot "$(DESTDIR)/usr/bin/aroot"
	install -d -m 0755 "$(DESTDIR)/etc/systemd/system"
	install -m 0644 aroot.service "$(DESTDIR)/etc/systemd/system/"
	install -d -m 0700 "$(DESTDIR)/etc/aroot" "$(DESTDIR)/etc/aroot/users"
	install -d -m 0700 "$(DESTDIR)/var/log/aroot"
uninstall:
	rm -f "$(DESTDIR)/usr/libexec/arootd" "$(DESTDIR)/usr/libexec/aroot-client" "$(DESTDIR)/usr/sbin/aroot-admin" "$(DESTDIR)/usr/bin/aroot" "$(DESTDIR)/etc/systemd/system/aroot.service"
clean:
	rm -f arootd aroot-client aroot-admin arootd-test aroot-client-test unit-tests

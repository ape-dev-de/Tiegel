<?php declare(strict_types = 1);

return [
	'lastFullAnalysisTime' => 1795680780,
	'meta' => array (
  'cacheVersion' => 'v12-linesToIgnore',
  'phpstanVersion' => '1.12.34',
  'phpVersion' => 80510,
  'projectConfig' => '{parameters: {level: 6, paths: [/var/www/html/src], tmpDir: /var/www/html/build/phpstan-tmp}}',
  'analysedPaths' => 
  array (
    0 => '/var/www/html/src',
  ),
  'scannedFiles' => 
  array (
  ),
  'composerLocks' => 
  array (
    '/var/www/html/composer.lock' => '08ca2cb90f4fca35e2db82f67fadc030fe1964d6',
  ),
  'composerInstalled' => 
  array (
    '/var/www/html/vendor/composer/installed.php' => 
    array (
      'versions' => 
      array (
        'phpstan/phpstan' => 
        array (
          'pretty_version' => '1.12.34',
          'version' => '1.12.34.0',
          'reference' => '4dd89ca7aa30fdc6760be21550d583bcc32e8476',
          'type' => 'library',
          'install_path' => '/var/www/html/vendor/composer/../phpstan/phpstan',
          'aliases' => 
          array (
          ),
          'dev_requirement' => true,
        ),
      ),
    ),
  ),
  'executedFilesHashes' => 
  array (
    'phar:///var/www/html/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/Attribute.php' => 'eaf9127f074e9c7ebc65043ec4050f9fed60c2bb',
    'phar:///var/www/html/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/ReflectionAttribute.php' => '0b4b78277eb6545955d2ce5e09bff28f1f8052c8',
    'phar:///var/www/html/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/ReflectionIntersectionType.php' => 'a3e6299b87ee5d407dae7651758edfa11a74cb11',
    'phar:///var/www/html/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/ReflectionUnionType.php' => '1b349aa997a834faeafe05fa21bc31cae22bf2e2',
  ),
  'phpExtensions' => 
  array (
    0 => 'Core',
    1 => 'FFI',
    2 => 'PDO',
    3 => 'PDO_ODBC',
    4 => 'Phar',
    5 => 'Reflection',
    6 => 'SPL',
    7 => 'SimpleXML',
    8 => 'Zend OPcache',
    9 => 'bcmath',
    10 => 'bz2',
    11 => 'calendar',
    12 => 'ctype',
    13 => 'curl',
    14 => 'date',
    15 => 'dba',
    16 => 'dom',
    17 => 'exif',
    18 => 'fileinfo',
    19 => 'filter',
    20 => 'ftp',
    21 => 'gd',
    22 => 'gettext',
    23 => 'gmp',
    24 => 'hash',
    25 => 'iconv',
    26 => 'intl',
    27 => 'json',
    28 => 'ldap',
    29 => 'lexbor',
    30 => 'libxml',
    31 => 'mbstring',
    32 => 'mongodb',
    33 => 'mysqli',
    34 => 'mysqlnd',
    35 => 'odbc',
    36 => 'openssl',
    37 => 'pcntl',
    38 => 'pcre',
    39 => 'pdo_dblib',
    40 => 'pdo_mysql',
    41 => 'pdo_pgsql',
    42 => 'pdo_sqlite',
    43 => 'pgsql',
    44 => 'posix',
    45 => 'random',
    46 => 'readline',
    47 => 'redis',
    48 => 'session',
    49 => 'shmop',
    50 => 'snmp',
    51 => 'soap',
    52 => 'sockets',
    53 => 'sodium',
    54 => 'sqlite3',
    55 => 'standard',
    56 => 'sysvmsg',
    57 => 'sysvsem',
    58 => 'sysvshm',
    59 => 'tidy',
    60 => 'tokenizer',
    61 => 'uri',
    62 => 'xml',
    63 => 'xmlreader',
    64 => 'xmlwriter',
    65 => 'xsl',
    66 => 'zip',
    67 => 'zlib',
  ),
  'stubFiles' => 
  array (
  ),
  'level' => '6',
),
	'projectExtensionFiles' => array (
),
	'errorsCallback' => static function (): array { return array (
  '/var/www/html/src/Greeter.php' => 
  array (
    0 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Call to an undefined method Acme\\Greeter::missing().',
       'file' => '/var/www/html/src/Greeter.php',
       'line' => 13,
       'canBeIgnored' => true,
       'filePath' => '/var/www/html/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => NULL,
       'nodeLine' => 13,
       'nodeType' => 'PhpParser\\Node\\Expr\\MethodCall',
       'identifier' => 'method.notFound',
       'metadata' => 
      array (
      ),
    )),
    1 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Parameter #1 $string of function strlen expects string, int given.',
       'file' => '/var/www/html/src/Greeter.php',
       'line' => 18,
       'canBeIgnored' => true,
       'filePath' => '/var/www/html/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => NULL,
       'nodeLine' => 18,
       'nodeType' => 'PhpParser\\Node\\Expr\\FuncCall',
       'identifier' => 'argument.type',
       'metadata' => 
      array (
      ),
    )),
    2 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Call to an undefined method Acme\\Greeter::alsoMissing().',
       'file' => '/var/www/html/src/Greeter.php',
       'line' => 23,
       'canBeIgnored' => true,
       'filePath' => '/var/www/html/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => NULL,
       'nodeLine' => 23,
       'nodeType' => 'PhpParser\\Node\\Expr\\MethodCall',
       'identifier' => 'method.notFound',
       'metadata' => 
      array (
      ),
    )),
    3 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Method Acme\\Greeter::items() return type has no value type specified in iterable type array.',
       'file' => '/var/www/html/src/Greeter.php',
       'line' => 26,
       'canBeIgnored' => true,
       'filePath' => '/var/www/html/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => 'See: https://phpstan.org/blog/solving-phpstan-no-value-type-specified-in-iterable-type',
       'nodeLine' => 26,
       'nodeType' => 'PHPStan\\Node\\InClassMethodNode',
       'identifier' => 'missingType.iterableValue',
       'metadata' => 
      array (
      ),
    )),
    4 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Method Acme\\Greeter::shout() should return int but returns string.',
       'file' => '/var/www/html/src/Shouting.php (in context of class Acme\\Greeter)',
       'line' => 11,
       'canBeIgnored' => true,
       'filePath' => '/var/www/html/src/Greeter.php',
       'traitFilePath' => '/var/www/html/src/Shouting.php',
       'tip' => NULL,
       'nodeLine' => 11,
       'nodeType' => 'PhpParser\\Node\\Stmt\\Return_',
       'identifier' => 'return.type',
       'metadata' => 
      array (
      ),
    )),
  ),
); },
	'locallyIgnoredErrorsCallback' => static function (): array { return array (
); },
	'linesToIgnore' => array (
),
	'unmatchedLineIgnores' => array (
),
	'collectedDataCallback' => static function (): array { return array (
); },
	'dependencies' => array (
  '/var/www/html/src/Greeter.php' => 
  array (
    'fileHash' => '7256d7b5bb18db65a6d34c55392b80bec2219c14',
    'dependentFiles' => 
    array (
    ),
  ),
  '/var/www/html/src/Shouting.php' => 
  array (
    'fileHash' => 'f78a426e95a89305bc3b4b1baf8be8f769fa89da',
    'dependentFiles' => 
    array (
      0 => '/var/www/html/src/Greeter.php',
    ),
    'usedTraitDependentFiles' => 
    array (
      0 => '/var/www/html/src/Greeter.php',
    ),
  ),
),
	'exportedNodesCallback' => static function (): array { return array (
  '/var/www/html/src/Greeter.php' => 
  array (
    0 => 
    \PHPStan\Dependency\ExportedNode\ExportedClassNode::__set_state(array(
       'name' => 'Acme\\Greeter',
       'phpDoc' => NULL,
       'abstract' => false,
       'final' => true,
       'extends' => NULL,
       'implements' => 
      array (
      ),
       'usedTraits' => 
      array (
        0 => 'Acme\\Shouting',
      ),
       'traitUseAdaptations' => 
      array (
      ),
       'statements' => 
      array (
        0 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'greet',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'string',
           'parameters' => 
          array (
            0 => 
            \PHPStan\Dependency\ExportedNode\ExportedParameterNode::__set_state(array(
               'name' => 'name',
               'type' => 'string',
               'byRef' => false,
               'variadic' => false,
               'hasDefault' => false,
               'attributes' => 
              array (
              ),
            )),
          ),
           'attributes' => 
          array (
          ),
        )),
        1 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'count',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'int',
           'parameters' => 
          array (
          ),
           'attributes' => 
          array (
          ),
        )),
        2 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'ignored',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'int',
           'parameters' => 
          array (
          ),
           'attributes' => 
          array (
          ),
        )),
        3 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'items',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'array',
           'parameters' => 
          array (
          ),
           'attributes' => 
          array (
          ),
        )),
      ),
       'attributes' => 
      array (
      ),
    )),
  ),
  '/var/www/html/src/Shouting.php' => 
  array (
    0 => 
    \PHPStan\Dependency\ExportedNode\ExportedTraitNode::__set_state(array(
       'name' => 'Acme\\Shouting',
       'phpDoc' => NULL,
       'usedTraits' => 
      array (
      ),
       'traitUseAdaptations' => 
      array (
      ),
       'statements' => 
      array (
        0 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'shout',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'int',
           'parameters' => 
          array (
            0 => 
            \PHPStan\Dependency\ExportedNode\ExportedParameterNode::__set_state(array(
               'name' => 'text',
               'type' => 'string',
               'byRef' => false,
               'variadic' => false,
               'hasDefault' => false,
               'attributes' => 
              array (
              ),
            )),
          ),
           'attributes' => 
          array (
          ),
        )),
      ),
       'attributes' => 
      array (
      ),
    )),
  ),
); },
];
